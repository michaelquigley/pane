# thinking — work order

implementation plan for [[thinking]] (spec: `docs/future/thinking.md`). the plan is settled; this document grounds it in the actual code and slices it into reviewable stages.

## landing protocol

each stage is landed, then gated by terminus (`terminus review` over the stage's changes, up to 3 rounds to seek `clean`, fixing advisories as they can be fixed). a stage is done when no blocking findings remain; advisories that can be fixed cheaply are fixed in the stage, the rest are recorded in the project journal. michael makes all commits; agents never commit.

## naming and register

the spec uses "reasoning" for the wire and the upstream concept, and "thinking" for the protocol event and the ui. this split is load-bearing and is recorded here so the implementation does not blur it:

- wire and go: `reasoning` / `Reasoning` (the upstream field, both tolerated spellings, `Delta.Reasoning`)
- protocol and ui: `thinking` / `thinking_*` (the `thinking_delta` sse event, `Message.thinking`, `thinkingCollapsed`, `streamingThinking`, css classes)

two display-only guarantees, both pinned by tests:

1. the backend `llm.Message` type has no reasoning field. the in-flight `messages` slice that `RunToolLoop` re-sends on tool-loop iterations therefore cannot carry it. this is the upstream-side guarantee; the type itself is the mechanism.
2. the frontend strips `thinking` and `thinkingCollapsed` from every message at the exact point the `/api/chat` request body is built in `executeRequest` (not earlier). `retryLastRequest` re-enters `executeRequest`, so it inherits the strip.

accepted residual (record in `docs/current/` with the stage 3 docs pass): thinking text is stored in localStorage with no cap. a conversation with a thinking-heavy model grows accordingly; revisit when a conversation approaches the storage quota in real use.

## stage 1 — backend: parse, protocol, pass-through

`internal/llm/types.go`

- `Delta` gains `Reasoning *string`, the pane-canonical field for the turn's thinking token (spec: "the field name in pane's own type is `reasoning`").
- because one pane field must accept two wire spellings, `Delta` gets a custom `UnmarshalJSON`: decode `content`, `tool_calls`, `reasoning`, and `reasoning_content` from the wire; `Reasoning` takes `reasoning` when present, else `reasoning_content`, else stays nil. a local wire struct inside the method carries the four fields; no exported type changes beyond `Reasoning`.
- `Message` is untouched. no reasoning field, by invariant 1.

`internal/sse/writer.go`

- `ThinkingDeltaData struct { Content string }`, structurally parallel to `DeltaData`.
- the event type string is `thinking_delta`. no other event data types change.

`internal/llm/toolloop.go`

- in the per-round stream loop, beside the content-token block: when `delta.Reasoning` is non-nil and non-empty, `sw.Send("thinking_delta", sse.ThinkingDeltaData{Content: *delta.Reasoning})`.
- that is the entire backend change. no accumulation on the backend — the per-round reasoning exists only as emitted events, and the frontend is the party that attaches it to committed messages (spec, "rounds and the tool loop"). the `round_complete` payload is unchanged in shape, which is what lets the frontend own the graft.

tests (extend the existing files; they already cover stream parsing and tool-loop event sequences with a fake upstream and recorded events)

- `internal/llm/stream_test.go` or a new `delta_test.go`: unmarshal raw wire lines carrying `reasoning` alone, `reasoning_content` alone, both (prefer `reasoning`), and neither; alongside, `content` and `tool_calls` still parse. also round-trip: a marshaled `Delta` with `Reasoning` set re-parses to the same value.
- `internal/llm/toolloop_test.go`: a fake upstream that emits reasoning deltas interleaved with content and tool calls across two rounds. assert: `thinking_delta` events appear in stream order; the assistant message in `round_complete` carries no reasoning; and — the load-bearing assertion — the request body of the round-2 `/chat/completions` call contains no `reasoning` field anywhere, proving the re-sent history is clean.
- `internal/api/chat_test.go`: a `/api/chat` request body whose messages include a `reasoning` (or `thinking`) field; assert the upstream client receives messages without it (invariant 2's server-side half: `encoding/json` drops unknown fields on `llm.Message`).

docs (the part of `docs/current/pane.md` that describes the protocol the backend now emits)

- the sse protocol section: add `thinking_delta` to the event list and the lifecycle prose — it interleaves with `delta` and tool-call events in upstream order, one per thinking token, per round; `round_complete` is unchanged.
- a short note where the design decisions live: display-only by construction (invariant 1), both upstream spellings tolerated.

## stage 2 — frontend: state, rendering, persistence

`ui/src/types.ts`

- `Message` gains `thinking?: string` and `thinkingCollapsed?: boolean` (undefined means expanded; the value persists in the conversation's localStorage entry).
- `SSEEvent` gains `| { type: 'thinking_delta'; content: string }`.

`ui/src/hooks/useChat.ts`

- `streamingThinking` state, and a `thinkingAccum` local in `executeRequest`, both mirroring the existing `streamingContent` / `contentAccum` discipline exactly: reset at request start, on `round_complete`, on `done`, and in the error/abort cleanup paths.
- new `thinking_delta` case: append to `thinkingAccum`, `setStreamingThinking`.
- `round_complete`: attach the accumulated thinking to the committed assistant message — `thinking: thinkingAccum` when non-empty, omitted when the round had none — composed with the existing `attachToolCallResults` so both land on the same message object.
- request body: build the wire messages by stripping `thinking` and `thinkingCollapsed` from each message at the `JSON.stringify` site (invariant 2).
- a `setThinkingCollapsed(messageIndex, collapsed)` callback, for the collapse toggle to call. it must use the functional update form — `setMessages(current => current.map(...))` — and must never map over a captured `messages` value: a captured-array replacement can clobber a tool-loop round that commits between capture and set, silently losing the newly committed message (mercurius round 1, C1). the functional form cannot go stale, and indices remain valid because rounds only append. it also disturbs no in-flight request, since messages are only read at request build time.
- return `streamingThinking` and `setThinkingCollapsed` from the hook.

`ui/src/components/ChatView.tsx`

- pass `streamingThinking` to the streaming `MessageBubble`.
- add `streamingThinking` to the auto-scroll dependency list (it currently keys on `messages`, `streamingContent`, `activeToolCalls`), so a thinking-only round with no content and no tool calls still scrolls the reader along.

`ui/src/components/MessageBubble.tsx` plus a new `ui/src/components/ThinkingBlock.tsx`

- `ThinkingBlock` renders the quiet block: a short label, the text, a collapse control. collapsed state is one line — label plus a truncated fragment of the block's opening (first non-empty line, or its leading characters, elided). no animation anywhere in it.
- render order in the assistant bubble: thinking block first, then tool calls, then content. that places each round's thinking where the model actually reasoned — before the call it motivated (spec scenario "the tool turn").
- committed message: render the block when `message.thinking` is non-empty; the toggle calls the hook's `setThinkingCollapsed`; initial state from the persisted flag, expanded by default. a message without thinking renders exactly as today — no placeholder, no affordance (spec scenario "the quiet model").
- streaming: when `streamingThinking` is non-empty the block is live and always expanded, no toggle. extend the current empty-round fallback (cursor shown when there is no content and no tool calls) so the thinking block shows whenever it has content, and the bare cursor remains only when both are empty.
- the thinking text is plain text, not markdown — it is the model's raw scratch, and rendering it as markdown invites the model's formatting to wear the conversation's typography. render in a monospace or dimmed secondary style, pre-wrap.

`ui/src/index.css`

- `.thinking-block` and friends, tuned to sit visibly quieter than `.assistant-content`: secondary weight, dimmer, smaller. follow the existing message and tool-call visual language in the file; no new fonts.

`ui/src/lib/exportMarkdown.ts`

- no change. it reads only `message.content` for user/assistant messages, so reasoning is skipped by construction (spec seam: "markdown export — reasoning skipped"). verify by reading; the stage's acceptance includes an exported markdown of a thinking conversation containing no thinking text.

## stage 3 — integration, verification, release notes

- `make build` (ui first, then the binary — the embed rule).
- live verification against ninfer with a qwen3 model: capture the raw upstream stream once and confirm which spelling is actually on the wire (the design tolerates both, so this confirms rather than decides). then, through the built binary: a turn streams thinking live; the block rests expanded at turn end; collapsing it and reloading returns the conversation with the block still collapsed; a tool-loop turn shows one block per reasoning round, each above the call it motivated; collapse a committed block while a tool-loop turn is in flight and confirm the round's committed message still lands; a model with no reasoning renders an unchanged conversation; the markdown export of a thinking conversation contains no thinking text.
- `docs/current/pane.md`: the frontend half of the protocol story — the `useChat` state machine handles `thinking_delta`, committed messages carry `thinking` and the collapse flag into localStorage, the request builder strips both fields. plus the accepted residual (uncapped localStorage growth from thinking text).
- the sse protocol line in `AGENTS.md` and its copy `CLAUDE.md` both list the event types; update both so the pair stays in sync (the canonical-doc question is open in the project journal — do not resolve it here).
- `CHANGELOG.md`: an `## Unreleased` `FEATURE` entry — model thinking streamed and displayed per message, persisted with the conversation, display-only by design.
- project journal entry recording the arc: what landed per stage, the terminus rounds and dispositions, the wire-spelling confirmation, anything deferred.

## invariants for review

terminus and michael should be able to check each stage against these:

1. no reasoning byte in any upstream request body, in any tool-loop round, on any path (`sendMessage`, `retryLastRequest`).
2. `llm.Message` has no reasoning field; the type is the guarantee.
3. the `round_complete` payload is unchanged in shape; the frontend grafts thinking onto the committed assistant message and owns it from there to localStorage.
4. a response without thinking tokens changes nothing the reader sees.
5. the collapse state of every block survives a reload, per message.
6. the register split holds: `reasoning` on the wire and in go, `thinking` in the protocol and the ui.