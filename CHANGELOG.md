# CHANGELOG

## Unreleased

FEATURE: pane now tracks exact context usage reported by the upstream and shows a compact percentage beside the model selector, with cool, warm, and hot bands as the configured window fills. each round's raw prompt, completion, and total token counts pass through a dedicated `usage` SSE event and ride with the conversation in browser storage. operators provide per-model context windows and an optional default in `pane.yaml`; `include_usage: false` is the escape hatch for endpoints that reject OpenAI's `stream_options` request field.

FEATURE: Model thinking is now streamed and displayed. assistant messages that carry reasoning render a quiet thinking block above their content — live while the turn streams, resting expanded at turn end, collapsible by the reader with the collapsed state persisting per message across reloads. the upstream stream reader tolerates both known reasoning field spellings (`reasoning` and `reasoning_content`), so any OpenAI-compatible backend that streams reasoning under either gets the display. in a tool loop, each round's block sits above the tool calls that round motivated. thinking is display-only by design: it is never returned to the model, and a response with no reasoning tokens renders exactly as before. markdown export skips it.

FIX: pane no longer rejects post-tool-turn chat requests when an assistant tool-call message has explicit null content. `dd v1.0.3` and the field-level `+nullable` contract preserve the valid OpenAI message shape while keeping request binding in `dd`.

## v0.1.0

Initial release.
