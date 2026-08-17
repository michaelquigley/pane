---
title: reasoning token accounting
state: horizon
created: 2026-08-16
tags: [feature]
subsystems: [backend]
---

Usage reporting that distinguishes thinking tokens from content tokens, for cost awareness. the first lookup is answered by the context usage arc's live pass (journal 2026-08-17): ninfer's final chunk carries only the three scalars — `prompt_tokens`, `completion_tokens`, `total_tokens` — no `completion_tokens_details`, no reasoning breakdown. the remaining work is growing the `usage` event's pass-through with the details fields (a backend struct field plus the event data field; the meter keeps reading the scalars) and deciding where the split surfaces once an endpoint reports it.

## why

The thinking display arc (built 2026-08, journal 2026-08-16) deferred it: nothing in the display depends on it. the context usage tracking arc (journal 2026-08-17) landed the usage event and answered the endpoint-shape question that blocked this card; nothing depends on the split yet, so it rests.