# thinking

the model's reasoning, shown in the conversation. this spec covers streaming reasoning tokens from the backend through the sse protocol to the ui, persisting them with the conversation, and rendering them as a quiet companion block above the assistant's content.

it is not a status surface, and not a steering surface. the value is comprehension: the thinking helps the reader understand more deeply what the model is doing, and it is helpful background in a lot of cases. it is attached to the message rather than being a live event to watch... though it does stream live while the turn is in flight, and following it mid-turn is a legitimate way to use it. a reader who wants only the answer can ignore it or collapse it, and the answer's presentation is never degraded by its presence.

## the first consumer

qwen3 behind ninfer. pane talks to ninfer directly — raw model access, no gateway in between — and qwen3-style backends stream reasoning tokens inside the chat completion delta, under a field spelling the backend chooses. the design is not qwen-specific: pane tolerates the known reasonings field spellings in the upstream delta, and any OpenAI-compatible backend that emits reasoning under one of them gets the display for free. a captured stream from ninfer with a qwen3 model confirms which spelling is actually on the wire; tolerating both means the design does not wait on that answer.

## what the reader sees

an assistant message that carries thinking renders a quiet block above its content. the block has a short label and no animation. while the turn streams, the block grows live, and the message list's existing auto-scroll carries it as it does content. when the turn completes, the block remains expanded — the thinking is what the reader may most want to read — and collapsing it is a deliberate user action on the block itself.

a message with no thinking renders exactly as it does today. the block is a property of the response, not a ui feature that can be turned on, and a model that never reasons produces a conversation indistinguishable from the one it produces today.

the collapsed state is one line: the label plus a truncated fragment of the block's opening. that is enough to recognize a block the reader collapsed before without reopening it.

## the wire

three surfaces, and they are deliberately thin.

**upstream.** the stream reader tolerates reasoning under both known delta field spellings, `reasoning` (openai o-style) and `reasoning_content` (the vllm / sglang family qwen3 backends typically use). both tolerated, one emitted: if a chunk carries either, pane treats it as thinking. the field name in pane's own types is `reasoning`.

**pane's sse protocol.** a new event type, `thinking_delta`, structurally parallel to `delta`: same shape, one string payload. it streams interleaved with `delta` and the tool call events in whatever order the upstream emits them. no other protocol events change.

**the request path.** thinking is display-only. it is never returned to the model: the frontend strips the field when it builds the `/api/chat` request body, and the backend's `Message` type carries no reasoning field, so a stray one cannot leak through to ninfer. with the stateless full-history design, echoing prior turns' reasoning back would re-send every turn's entire thinking chain on every request, and a qwen3 thinking chain runs several times longer than its answer. on long conversations that is real, compounding prompt size for a benefit the reader cannot see. v1 does not pay it. the decision is reversible without a migration, because v1 persists the thinking the upgrade would need (see the seam census).

there is also no thinking control. pane sends a plain chat completion request; whether the model thinks is what the model does, and model-specific extras (`enable_thinking`, chat template knobs) do not belong in pane with no gateway layer to configure them on.

## rounds and the tool loop

thinking arrives per backend round: the model thinks, then answers or calls tools, and on a tool-heavy turn it thinks again in the next round. the display follows that structure honestly — each round's thinking block sits with that round's assistant message, adjacent to the tool calls it motivated, so the reader sees the model reason its way *into* a call, not just receive its result. on a simple single-round turn, which is the common case, this is exactly one block above the answer.

on the backend, reasoning accumulates per round into the round's assistant message, exactly as content does. on the frontend, the committed assistant message for a round carries that round's reasoning, and because the backend does not echo it in the `round_complete` payload, the frontend is the party that attaches its locally accumulated reasoning to the committed message. the frontend owns reasoning's life from stream to commit to storage; the backend is pass-through only.

an interrupted turn behaves exactly as it does today: what committed stays — with its thinking — and what did not commit is discarded. thinking introduces no new recovery path.

## persistence

reasoning lives on the frontend `Message` type alongside `content`, persists in the conversation's localStorage entry like everything else, and is grafted onto assistant messages at commit. the collapsed/expanded state of each block is stored with the message, so a reload returns the conversation exactly as the reader left it — a collapsed block stays collapsed, an expanded one stays open.

the markdown export skips reasoning. it is background to the message, not content of it; a reader who wants the thinking has the conversation itself.

## scenarios

**the read.** a question goes to a qwen3 model. a block begins to grow above the answer as the model works, and the reader either follows it or scrolls past to the answer as it lands. the turn ends with both visible, the block expanded. the reader finishes the answer, reads two paragraphs of the thinking, collapses the block, and moves on. days later the conversation is reopened in the same browser, and the block is exactly as left: collapsed, one line, recognizable.

**the tool turn.** the model is asked to inspect a repository. it thinks, calls a tool, reads the result, thinks again, and answers. the conversation shows the first thinking block, the tool call and its result, the second thinking block, then the answer. the mid-turn reasoning stays with the turn, and each block sits where the model was actually reasoning — the reader can see why the second tool call happened, which is usually the part worth understanding.

**the quiet model.** the reader switches the conversation to a model that does not emit reasoning. nothing changes. no empty blocks, no placeholders, no affordance for a capability the response did not use.

## seam census

the boundary decisions this design makes, recorded so review and implementation inherit the call rather than reconstructing it.

| boundary | call | why | revisit when |
|---|---|---|---|
| prior-turn reasoning echoed in re-sent history | display-only | stateless full-history re-sends multiply prompt size by thinking length, which runs several times longer than content; the cost is on every request, the putative benefit is unverified | long multi-turn coherence degrades on qwen3, or ninfer / model guidance says the echo is required |
| upstream wire spelling | tolerate both `reasoning` and `reasoning_content` | the spelling is ninfer's, not pane's; tolerating both is one line and removes the guess from the design | a backend emits a third shape |
| thinking control | none; the model decides | pane talks to ninfer directly; model-specific request extras belong to a layer pane does not have | a model a reader cares about defaults thinking off and they want it on |
| collapse state | persisted per message, survives reload | the reader's position in a block is part of the session, like the conversation itself | — |
| markdown export | reasoning skipped | it is background to the message, not content of it | a reader asks for it |
| request wire | stripped in the frontend, absent from the backend `Message` type | display-only enforced at both layers, so the contract does not depend on one of them | round-trip is adopted |

## deferred (and why)

- **round-trip echo of reasoning in the re-sent history.** the v1 call is display-only, and the seam census records when to reopen it. v1 persisting the thinking is what keeps the upgrade a small change rather than a data migration.
- **per-model thinking control.** forcing thinking on or off would mean model-specific request extras, which needs a config surface and knowledge of what each backend accepts. until a model a reader actually uses defaults thinking off, the knob has no user.
- **other reasoning wire formats.** structured reasoning blocks and vendor-specific shapes beyond the two tolerated spellings. the tolerance list is deliberately short; a third shape is a real design question, not a parser line.
- **reasoning in the markdown export.** skipped on purpose, per the seam census. exporting it is a one-line change when wanted.
- **reasoning token accounting.** usage reporting that distinguishes thinking tokens from content tokens, for cost awareness. ninfer's usage shape would need looking at first; nothing in the display depends on it.