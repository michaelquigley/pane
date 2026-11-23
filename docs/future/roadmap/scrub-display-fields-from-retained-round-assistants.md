---
title: scrub display fields from retained round assistants
state: inbox
created: 2026-11-23
tags: [defect]
source: terminus
log: docs/journal/2026-11-23.md
---

`buildChatRequest` in `ui/src/lib/turnRecord.ts` runs `wireMessage` over top-level messages, which strips `thinking`, `thinkingCollapsed`, and `tool_call_results`. `wireRecord` only strips `partial_text`, so a retained round assistant (`turns[].rounds[].assistant`) carrying display fields would send them in `recovery`. apply `wireMessage` to each retained round assistant in `wireRecord`, leaving the stored record and its continuation envelope unchanged. add a focused serializer test. the browser-written fixtures in `internal/api/testdata/recovery/` should come out unchanged; if one changes, review that diff rather than regenerating it blindly.

## why

terminus review '8bf50b1b313d' raised this as blocking, and michael vetoed it out of the composer-performance change on 2026-11-23. it is latent today. round assistants come only from the backend's `round_ready` event, which has no display fields; the browser decorates only the promoted top-level copy; and the Go intake has no `thinking` field to bind. only a hand-edited or foreign session file reaches it. it becomes live if the backend message ever gains a reasoning field, which is the plan in the [round-trip reasoning echo](round-trip-reasoning-echo-in-re-sent-history.md) card.
