# context usage tracking — work order

implementation translation of `docs/future/context-usage-tracking.md`, grounded in the current code. the arc lands a `usage` SSE event that stops discarding the upstream's usage chunk, a backend-side `stream_options` request knob with a config escape hatch, the operator's context windows in `pane.yaml` surfaced verbatim through `/api/config`, a usage record that rides the conversation object, and a compact context meter in the header.

three stages, each terminus-gated to clean before michael's commit, per the standing protocol:

1. backend — config, client, types, protocol, tool loop, `/api/config`, tests
2. frontend — types, `useChat` record state, `App` wiring, the meter
3. integration — `docs/current`, `AGENTS.md`, config templates, changelog, live verification

## stage 1 — backend

### config — `internal/config/config.go`

`Config` gains three fields. no dd tags are needed — CamelCase binds to the snake_case keys, and none requires `+required`, `+extra`, or a name override:

```go
ContextWindows       map[string]int // context_windows
DefaultContextWindow int            // default_context_window
IncludeUsage         bool           // include_usage
```

- `DefaultConfig()` sets `IncludeUsage: true`; the window fields stay zero/nil — absence means "the operator provided no window", which the frontend reads as unknown, not as zero.
- `Validate()`: every `ContextWindows` value must be > 0 (a zero window makes the frontend's percentage meaningless, so reject it at load rather than dividing by it later); `DefaultContextWindow` must be > 0 when non-zero (zero reads as unset).
- cascade and merge semantics are unchanged. `include_usage: false` in any layer overrides the compiled default, exactly the way the existing `Approve bool` on MCP server config works.

### client — `internal/llm/client.go`, `cmd/pane/main.go`

- `NewClient` gains an `includeUsage bool` parameter and stores it on `Client` as `IncludeUsage bool`. update the call sites: `main.go` (passes `cfg.IncludeUsage`) and the llm/api test files that construct a client.
- `StreamChat` sets the request knob beside the existing `chatReq.Stream = true` override:

```go
if c.IncludeUsage {
    chatReq.StreamOptions = &StreamOptions{IncludeUsage: true}
}
```

- the client owns the wire contract: `RunToolLoop`'s signature is unchanged, and every round's request carries the knob because it is set per request in `StreamChat`, not as sticky state.
- flag off → `StreamOptions` stays nil, `omitempty` drops the key, and the marshaled body is byte-identical to today's request.

### types — `internal/llm/types.go`

```go
// ChatRequest gains:
StreamOptions *StreamOptions `json:"stream_options,omitempty"`

type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// StreamChunk gains:
Usage *Usage `json:"usage,omitempty"`

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
```

- `Usage` is exactly the three upstream scalars. `*_tokens_details` sub-objects do not land in this arc; the reasoning-token card adds them to the pass-through when it lands.

### protocol — `internal/sse/writer.go`

```go
type UsageData struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
```

structurally parallel to `DeltaData`. the event type is `usage`.

### tool loop — `internal/llm/toolloop.go`

the existing `if len(chunk.Choices) == 0 { continue }` skip becomes a guard, so a chunk whose choices are empty but which carries usage is not discarded — and the usage emit sits after the chunk's choices processing:

```go
if len(chunk.Choices) > 0 {
	delta := chunk.Choices[0].Delta

	// existing content / thinking / tool-call handling, unchanged
}

// emitted after the chunk's choices so a combined chunk keeps the
// documented wire order: its deltas first, then usage
if chunk.Usage != nil {
	_ = sw.Send("usage", sse.UsageData{
		PromptTokens:     chunk.Usage.PromptTokens,
		CompletionTokens: chunk.Usage.CompletionTokens,
		TotalTokens:      chunk.Usage.TotalTokens,
	})
}
```

- the check runs for every chunk, so an endpoint that rides usage on a chunk that also carries choices still fires the event — the same wire-tolerance posture as the reasoning-spelling handling. because the emit follows the choices processing, the documented order holds for that shape too: the chunk's own `delta` and tool-call events precede its `usage` event.
- ordering falls out of the stream: upstreams send the usage chunk as the final data line before `[DONE]`, so the event lands after the round's content and tool-call events and before `round_complete`, with no extra sequencing. one event per round, when the upstream sends one.
- absent usage: no event, the loop is unchanged. a turn is never broken by an absent optional number.
- the loop does no arithmetic on the numbers — no percentage, no window lookup, no derived field. the backend's hands carry only the upstream's numbers.

### api — `internal/api/api.go`

`handleConfig` moves from the `map[string]string` literal to a named struct (the window fields are not strings):

```go
type configResponse struct {
	DefaultSystem        string         `json:"default_system"`
	DefaultModel         string         `json:"default_model"`
	MCPSeparator         string         `json:"mcp_separator"`
	ContextWindows       map[string]int `json:"context_windows,omitempty"`
	DefaultContextWindow int            `json:"default_context_window,omitempty"`
}
```

`include_usage` is deliberately absent from the response — it is a backend-side request knob, and the frontend neither sends upstream requests nor needs to know whether pane asked. a nil `ContextWindows` is omitted from the body; a configured map passes through verbatim.

### stage 1 tests

- `stream_test.go`: a `{"choices":[],"usage":{...}}` line parses to `chunk.Usage != nil` with the three fields populated; a chunk carrying both choices and usage parses both; a chunk without usage leaves `Usage` nil.
- `toolloop_test.go`, extending the existing fake-upstream fixtures:
  - single-round turn whose stream ends with a usage chunk: assert the event order `delta* → usage → round_complete → done` — `usage` strictly before `round_complete`.
  - combined-chunk shape: a stream whose final chunk carries both a content delta and usage — the `delta` event precedes the `usage` event from that same chunk, then `round_complete`. the documented order holds for the tolerated nonstandard shape, not just the standard one.
  - two-round turn (round 1 emits a tool call): exactly one usage event per round, each before that round's `round_complete` — the spec's tick-up scenario.
  - request-body capture, the same pattern the thinking arc used for the reasoning-cleanliness checks: flag on → every round's body carries `"stream_options":{"include_usage":true}`, including the re-send after tool results; flag off → no `stream_options` byte in any body.
  - stream with no usage chunk: the event sequence is identical to today's — no new event anywhere, turn succeeds.
- config: default is on; `include_usage: false` in a merged file wins; validation rejects a zero or negative `context_windows` value and a negative `default_context_window`.
- `api_test.go`: `/api/config` with a configured window map returns it verbatim plus `default_context_window`; unconfigured → both keys absent; `include_usage` absent from the body in both flag states.

## stage 2 — frontend

no frontend test runner exists in this project (build is `tsc -b && vite build`, lint is eslint) — stage 2 is verified by type-check, lint, and the stage 3 live pass. the meter's derivation stays a pure function so it is reviewable and portable to a harness if one ever lands.

### types — `ui/src/types.ts`

- `ConfigResponse` gains `context_windows: Record<string, number>` and `default_context_window: number`.
- `SSEEvent` gains `| { type: 'usage'; prompt_tokens: number; completion_tokens: number; total_tokens: number }`.
- new `UsageRecord`:

```typescript
export interface UsageRecord {
  promptTokens: number
  completionTokens: number
  totalTokens: number
  model: string // the resolved model id the number was measured against
  at: number    // epoch ms of the usage event
}
```

- `Conversation` gains `usage?: UsageRecord | null` — last-wins, keyed by `model`, riding the conversation's localStorage entry. pre-existing conversations lack the field; the optional type is the whole migration.

### useConfig — `ui/src/hooks/useConfig.ts`

the mapping gains `context_windows: data.context_windows || {}` and `default_context_window: data.default_context_window || 0`. absence on the wire (an older backend) normalizes to "no window known", so the meter parks at `?` rather than misreading zero as a window.

### useChat — `ui/src/hooks/useChat.ts`

new state `usageRecord: UsageRecord | null`, managed like the streaming buffers:

- `executeRequest` start: reset to `null`. a send is a history mutation, so per the spec's invalidation rule the readout drops to `?` before the new turn's first usage event rather than holding the previous turn's number. `retryLastRequest` re-enters `executeRequest` and inherits the reset — a retry reads `?` until the retried turn reports, which is consistent with the send rule.
- `usage` event: `setUsageRecord({ promptTokens: event.prompt_tokens, completionTokens: event.completion_tokens, totalTokens: event.total_tokens, model: options.model, at: Date.now() })`. the last event wins, so the readout ticks up per round as tool output accumulates in context.
- `setMessages` (the abort/clear wrapper) resets to `null`.
- new `loadConversation(conv: Conversation | null)` method: clears any stale request error, sets the messages, and seeds `usageRecord` from `conv.usage ?? null` — the persisted record drives the meter on switch and reload without the hook touching storage. it performs no abort; callers abort separately, as they do today.
- `SendMessageOptions.model` now carries the resolved model id: `App.handleSend` sends `model: preferences.modelOverride || config.default_model` (previously `|| ''`). the record is stamped with the same id the window map keys on, and the meter's model-match rule no longer needs the backend to report back what it resolved — the event stays the upstream's numbers untouched. wire-identical in every real case, because the backend resolves `''` to that same configured default; if `/api/config` never loaded and there is no override, the record's model is `''`, matches no window, and the meter shows `?` — the same degraded state the UI already has in that situation.

### App — `ui/src/App.tsx`

- the conversation-switch sync effect calls `chat.loadConversation(activeConversation)` instead of `chat.setMessages(activeConversation.messages)` (the empty case keeps `chat.setMessages([])`). the seed makes the save idempotent on switch — live state equals persisted state after a load — so the existing `skipNextConversationSyncRef` mechanism needs no extension.
- the conversation-save effect extends: deps become `[chat.messages, chat.usageRecord]` and the write includes `usage: chat.usageRecord`. a mid-turn usage event persists the record the moment it lands; a request-start reset persists the invalidation, because the old record was measured against pre-send history.
- the header gains `<ContextMeter>` directly after `<ModelSelector>` — it is a property of model × conversation, and that is where both already live. placement is a starting position, not a commitment.

### the meter — `ui/src/components/ContextMeter.tsx`

new quiet header component. a pure, colocated derivation function computes the display state:

```
no record                          → '?'   no usage reported yet
record.model !== selected model    → '?'   measured against a different model
no window for record.model         → '?'   no context window known for '<model>'
(exact map entry or positive default)
otherwise                          → pct   round(promptTokens / window * 100)
                                      band: cool < 50, warm 50–80, hot >= 80
```

- the percentage is unclamped (a value past 100 means the last accepted round already exceeded the configured window — information, not an error) and an integer.
- the `?` states carry no band hue (neutral). a `title` tooltip names the reason in every case, including the exact model id in the no-window case, so an operator sees the key to add to `context_windows`.
- no transitions, consistent with the chrome's restraint; the band hues draw on the existing palette's accent/warning vocabulary per the aesthetic direction. exact color tokens are an implementation detail.
- selected model is `preferences.modelOverride || config.default_model` — the same resolution the send path uses, so the record's model and the meter's comparison never disagree.

## stage 3 — docs, changelog, live verification

- `docs/current/pane.md`:
  - SSE protocol: the `usage` event with its payload line; a lifecycle note (once per round, after the round's content and tool-call events, before `round_complete`; absent when the config switch or the upstream declines); the `usage` node on the lifecycle mermaid's completion path.
  - the `/api/config` table row and schema: `context_windows` and `default_context_window` added; `include_usage` stated as backend-side only.
  - configuration section: the three new keys, same comment style as `pane.yaml.example`.
  - frontend types block: the `usage` SSEEvent line, `UsageRecord`, `Conversation.usage`, `ConfigResponse` additions.
  - state flow: a `usage` line in the per-event list; a short paragraph on the record's placement (rides the conversation object, seeds on load, resets on any history mutation, last-wins keyed by model); the meter in the UI bullets (header next to the model selector, the bands, the `?` states).
- `AGENTS.md` (`CLAUDE.md` is a symlink to it, so one edit keeps the pair in sync): the SSE protocol line gains `usage`; the package-structure `components/` listing gains `ContextMeter.tsx`; the `/api/config` table row reads "server config (system prompt, model, context windows)".
- `pane.yaml.example` and the `cmd/pane/new.go` template gain a commented block:

```yaml
# context windows per model id, for the header's context meter.
# models with no entry and no default show '?' in the meter.
#context_windows:
#  qwen2.5:14b: 32768
#default_context_window: 128000

# ask the upstream for token usage on every request (default true).
# set false for an endpoint that rejects the stream_options field.
#include_usage: false
```

- `CHANGELOG.md`: an Unreleased FEATURE entry in the house prose format — the meter, the pass-through event, the config keys, and the escape hatch.
- live pass, shaped like the thinking arc's: a temporary pane instance on :8401 against ninfer with a filesystem MCP server pointed at a /tmp repo; both torn down afterward.
  - a raw stream capture of the upstream's usage chunk, its verbatim shape recorded in the journal — that answers the reasoning-token card's first step (does ninfer report `completion_tokens_details` at all), so its re-run becomes a journal lookup rather than a new capture.
  - plain turn: the `usage` event lands and the meter's value matches `prompt_tokens` over the configured window.
  - tool turn: one usage event per round, the second larger (tool output now in context) — the readout ticks up.
  - escape hatch: a stub endpoint that 400s when the request body carries `stream_options` → the turn fails with `upstream_error` carrying the stub's body; flip `include_usage: false` → the same turn works and the meter stays at `?`.
  - `/api/config`: window keys present when configured, `include_usage` absent in every case.
- `make test` green, then `make build` so the installed binary embeds the new UI.
- manual pass left for michael (no headless browser in this environment): the meter's bands at real fill levels, `?` on first use, the model-switch `?`-then-number landing, reload restoring the readout, the stub-400 failure rendering in the conversation, and one narrowed invalidation check — stop mid-turn of a tool loop: the meter goes to `?` and stays there, with no flicker from a late buffered usage event (the only reset path whose safety rests on the current-request guard rather than synchronous ordering; send, retry, and clear share the same synchronous reset and need no dedicated check).

## invariants (what review checks)

1. **wire purity.** `llm` and `sse` carry only the upstream's numbers: `Usage` and `UsageData` are exactly the three scalars, and no percentage, window, or derivation exists anywhere in the backend. the percentage is computed only in the frontend's meter.
2. **pass-through.** the `usage` event fires when a chunk carries usage — on arrival, before the empty-choices skip — once per round when the upstream sends one. a turn is never broken by absent usage.
3. **the request contract.** `stream_options` is set on every request while `include_usage` is on (the default), and is absent byte-for-byte when it is off. no other request behavior changes.
4. **config separation.** `/api/config` surfaces the window keys verbatim and never surfaces `include_usage`; the frontend resolves windows and computes the percentage. the backend never mixes a config fact into a usage event.
5. **the record rides the conversation.** stored on the conversation object (localStorage today), seeded on load, reset on any history mutation (send, retry, clear, switch, abort), last-wins and keyed by the resolved model id. the display reads it through hook state that mirrors the conversation — never storage directly, never display-local scratch.
6. **honest display.** the percentage is `prompt_tokens` over the operator-configured window, unclamped and integral. bands: cool < 50, warm 50–80, hot ≥ 80. every `?` state is distinct and named in its tooltip.

## placement calls

- the meter sits directly after `ModelSelector` in the header — the spec's "next to the model selector", held as a starting position for the implementation phase.
- `include_usage` is a client-level knob (a `NewClient` parameter), not a parameter threaded through `RunToolLoop` — the client owns the wire contract, and the tool loop's signature stays stable.
- the record's `model` is stamped by the frontend from its own resolution (override or configured default). the backend never mixes request context into the pass-through event.
- one extended save effect persists the record (deps: messages + usageRecord). the `loadConversation` seed makes saves idempotent on conversation switch, so the existing skip flag covers everything and no second skip mechanism is added.