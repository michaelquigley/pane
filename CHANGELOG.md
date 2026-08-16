# CHANGELOG

## Unreleased

FEATURE: Model thinking is now streamed and displayed. assistant messages that carry reasoning render a quiet thinking block above their content — live while the turn streams, resting expanded at turn end, collapsible by the reader with the collapsed state persisting per message across reloads. the upstream stream reader tolerates both known reasoning field spellings (`reasoning` and `reasoning_content`), so any OpenAI-compatible backend that streams reasoning under either gets the display. in a tool loop, each round's block sits above the tool calls that round motivated. thinking is display-only by design: it is never returned to the model, and a response with no reasoning tokens renders exactly as before. markdown export skips it.

## v0.1.0

Initial release.
