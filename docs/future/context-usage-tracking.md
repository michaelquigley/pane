# context usage tracking

pane re-sends the entire conversation with every request, which means the context window fills up invisibly... the only feedback today is the upstream eventually refusing to cooperate. pi.dev shows the fill level as a percentage in its footer, and this spec brings the same to pane: a compact readout of what fraction of the model's context the current conversation occupies, measured exactly and resolved from the operator's own config.

## the shape of the problem

three facts make this easier in pane than in a typical client, and one makes it more urgent.

the first is the stateless re-send. because every `/api/chat` request carries the full history, the upstream's own `prompt_tokens` for a turn is, by construction, the exact size of the context the model just looked at. it is measured with the tokenizer that actually owns the window. no local tokenization, no estimation, no model catalog of tokenizer shapes... the number pane wants is a number the upstream already computes, and pane simply isn't asking for it yet.

the second is that the number is already on the wire, being thrown away. OpenAI-compatible streaming endpoints report usage in a final chunk whose `choices` array is empty, and the tool loop in `internal/llm/toolloop.go` skips exactly that shape. the fix is to stop discarding it.

the third is the one that makes it urgent. the tool loop appends tool results to the history and re-sends everything each round, so a single turn can quietly double the context. today there is no way to see how much a filesystem dump or a hive query cost. in a multi-round turn the usage event fires per round, and the readout ticks up visibly as the tool output accumulates.

## the reference: what pi does

pi's footer percentage is `tokens / contextWindow`. the numerator comes from the last assistant response's usage as reported by the upstream; the local-tokenization fallback pi carries exists only because pi auto-compacts, and a compaction gate needs a number even when the upstream goes quiet. the denominator comes from pi's curated model catalog.

pane can skip both crutches. the upstream usage is exact because of the re-send property, and the denominator is a config fact the operator already maintains. what pane adopts from pi is the shape of the display, not the machinery: a compact percentage in the chrome, outside the conversation itself.

## design

### the numerator: measured, not estimated

the request asks for usage. `ChatRequest` gains the OpenAI `stream_options` field, set to `{"include_usage": true}` on every chat request unless the operator has switched it off. `StreamChunk` gains the `usage` field (`prompt_tokens`, `completion_tokens`, `total_tokens`), and the tool loop stops treating the empty-choices chunk as noise: when a chunk carries usage, the backend emits it to the frontend and moves on.

the display's percentage uses `prompt_tokens` only — that is the context size, full stop. `completion_tokens` and `total_tokens` pass through for the record but play no role in the percentage today; they are where the reasoning-token work (see deferred) will find its home later.

missing usage degrades, it never fails. an endpoint that ignores `stream_options` produces no usage chunk, so no event fires, and the readout stays at its unknown state. a turn is never broken by an absent optional number.

### the denominator: pane.yaml

the context window is operator knowledge, and pane.yaml is where operator knowledge lives:

```yaml
context_windows:
  qwen3:32b: 32768
  some-gateway-model: 200000
default_context_window: 128000   # optional
```

resolution is exact match on the model id, then the default if set, then unknown. no prefix tricks, no built-in table of model windows, no scraping of `/v1/models` vendor fields. a curated table is the wrong shape for pane's audience... it talks to *any* OpenAI-compatible endpoint, most of which are self-hosted models no catalog has ever listed, and the person who configured the endpoint is the person who knows the window. one source of truth, owned by the operator.

the window keys surface verbatim through `/api/config` (alongside `default_model` and its friends), so the frontend resolves the window itself. the backend never mixes a config fact into a usage event.

### the wire: one new event, pure pass-through

a dedicated `usage` SSE event, emitted once per round when the upstream's usage chunk arrives — after the round's content and tool-call events, before `round_complete` (which waits on tool execution). its payload is the upstream's numbers untouched:

```
event: usage
data: {"prompt_tokens": 41230, "completion_tokens": 512, "total_tokens": 41742}
```

the event carries exactly those three scalar fields. upstream usage objects can hold more — `*_tokens_details` sub-objects for cached and reasoning tokens — and the pass-through deliberately stops at the scalars; the reasoning-token card adds details fields when it lands, which is what keeps that card nearly free.

no derived values cross the wire. the percentage is a display-side computation from two facts the frontend already holds — the record (from the event) and the window (from config). keeping the event a pure pass-through is what leaves room for the future: compaction and reasoning-token accounting will want the raw token facts in the backend's hands, and both are already there... usage from the stream, window from the config.

### the display

a compact meter in the header, next to the model selector — it is a property of model × conversation, and that is where both already live. it shows the percentage with a hue that warms as headroom runs down — cool up to 50% of the window, warm from 50 to 80%, hot past 80% — and a `?` for unknown.

the rules are deliberately honest:

- `?` until the first usage event arrives for the conversation.
- the readout reflects the last usage record the conversation carries, and only while that record's model is the selected model. switching models resets to `?` until the new model responds.
- any change to the history the number was measured against — editing, clearing, or appending (a send) — invalidates the record; a send drops the readout to `?` before the new turn's first usage event rather than holding the previous turn's number. today's UI clears, switches, and sends, all of which reset naturally; the rule is stated generally so a future edit affordance doesn't have to remember it.
- unknown window (model absent from the map, no default) shows `?` even with a valid record. the number is only as good as its denominator.

### the escape hatch

`stream_options` is a request contract pane imposes on whatever endpoint it talks to. the one real risk is an endpoint that rejects the field with a 400 — a cosmetic feature taking down the whole conversation. so the config gains `include_usage` (default on). one line in pane.yaml restores behavior identical to today for the endpoint that doesn't like it. the flag stays backend-side: it shapes the request, and `/api/config` never surfaces it, because the frontend neither sends upstream requests nor needs to know whether pane asked. it is consistent with the project's posture toward its operators: they know their endpoint better than the client does.

an endpoint that does reject the field is a different matter — that is a transport-tier failure, the turn fails with the usual `upstream_error`, and the fix is the toggle, not a retry. the two failure shapes are deliberately asymmetric: a 400 breaks the turn, an ignored flag doesn't.

### the record: rides with the conversation

the last usage for a conversation is stored *on the conversation object* — token counts, the model they were measured against, and when. the display reads through the conversation and never touches storage directly.

that placement is load-bearing for the next iteration of pane, where conversation storage moves out of the browser into a backend. today the record lives in localStorage because conversations do; when storage moves, the record moves with it as data — self-contained JSON that serializes with the conversation it describes. no re-derivation, no replaying of requests, no new estimation code. the alternative — display-local scratch state — would have to be thrown away and rebuilt at the migration, and rebuilding a measured number without the upstream's tokenizer is exactly the hole this design avoids.

## scenarios

michael has a 32k-window local model and a 200k-window gateway model in one pane.yaml. he asks the local model to walk a large corpus... the readout climbs through the turn, ticks up again after a tool round that read four files, and sits at 61% with the hue in the warm band. he knows, before asking anything else, that the next big read would push him toward the wall.

he switches the same conversation to the 200k model. the readout drops to `?` — the 61% was measured against a different model's window — and lands at a low single digit on the first response. the number is honest about what it means.

a self-hosted endpoint from a different vendor 400s on `stream_options`. the conversation dies at the transport tier, the log says so, and one line in pane.yaml (`include_usage: false`) brings it back. every turn works again, and the readout parks at `?`... an honest answer from an endpoint that declined to measure.

the browser reloads mid-project. the conversation list restores, and each conversation's readout restores with it, because the record is part of the conversation, not a session scratchpad.

## seam census

| boundary | call | why | revisit when |
|---|---|---|---|
| wire purity (model/transport) | `llm` and `sse` carry only wire facts: the usage struct and a pass-through event. no percentage, no window, no derivation anywhere in the backend | the thin-proxy identity; the display owns display math | a backend-side consumer (compaction) needs the derivation — it can compute from usage + config without any protocol change, so the call should survive |
| config fact vs display fact | `context_windows` surfaces verbatim through `/api/config`; the frontend resolves windows and computes the percentage; the usage event never carries config | one source of truth for the denominator; the event stays upstream-shaped | the same compaction consumer — it reads config directly, no seam crossed |
| imposed request contract | `include_usage: true` sent by default, with a config escape hatch | a cosmetic feature must not take down the turn; the operator owns knowledge of their endpoint | an endpoint family is found that needs more than a boolean |
| error by tier | endpoint 400 on `stream_options` → `upstream_error`, the turn fails (transport tier, same as any upstream HTTP error). usage chunk absent → no event, readout stays `?` (optional-data tier, turn unaffected) | the asymmetry is the design: rejection is a contract violation, silence is a capability gap | — |
| substrate ownership | the usage record lives on the conversation object; the display reads only through the conversation | browser storage today, backend storage next iteration; the record migrates as data | the backend-storage iteration itself — that is the recorded revisit condition |

## deferred (and why)

**auto-compaction.** pane's browser-owned history means a future compaction needs a backend→frontend contract to *replace* stored history — the backend cannot rewrite the browser's state on the user's behalf. that contract is out of scope here, and today's plumbing is deliberately necessary-but-not-sufficient for it: raw usage on the wire and windows in config are the two facts a compaction gate will want, and both land with this work.

**local token estimation.** pi carries an `estimateContextTokens` fallback for zero-usage responses because its compaction gate must fire even when the upstream goes quiet. pane has no gate, so `?` is the honest answer and the estimator stays out. revisit exactly when compaction is scoped — the two ship together.

**reasoning-token accounting.** the existing roadmap card (`reasoning-token-accounting.md`) wants the thinking/content token split for cost awareness. it is the same usage chunk: once this event lands, the card grows `completion_tokens_details` into the pass-through and is nearly free. the card's first step — looking at the endpoint's actual usage shape — should be re-run at that time.

**multi-model conversation records.** the record is last-wins, keyed by the model it was measured against; a conversation that bounces between models shows the last model's number or `?`. a per-model record map is the upgrade path if anyone actually lives in that pattern.

## what lands with it

- `internal/llm`: `StreamChunk` gains the usage field, `ChatRequest` gains `stream_options`, and the tool loop emits the usage event instead of discarding the chunk that carries it.
- `internal/sse`: the usage event data type.
- `internal/config` and `/api/config`: `context_windows` and `default_context_window` flow to the frontend verbatim; `include_usage` stays a backend-side request knob and never crosses to the frontend.
- `ui`: the SSE event, the config fields, the conversation record, and the header meter.
- `docs/current/pane.md`: the protocol's event list and the configuration section grow by the same amounts, so the built system catches up to its documentation.