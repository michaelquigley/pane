---
title: round-trip reasoning echo in re-sent history
state: researching
created: 2026-08-16
tags: [feature, spike]
subsystems: [backend]
milestone: v0.1.x
---

Echo prior turns' reasoning back in the re-sent chat history: add a reasoning field to the backend request `Message` type and include it in the re-sent messages. v1 persisted the thinking in the conversation (localStorage), so this is a small change — no data loss, no migration. The upstream spelling the model endpoint accepts for echoed reasoning would need confirming against ninfer, the same way the display capture confirmed `reasoning_content` on the wire.

## why

The thinking display arc (built 2026-08, journal 2026-08-16) settled on display-only: with pane's stateless full-history re-send, echoing prior turns' reasoning multiplies prompt size by thinking length on every request, for unverified benefit. The seam census records the revisit conditions: long qwen3 multi-turn coherence visibly degrades, or ninfer or model guidance says the echo is required.