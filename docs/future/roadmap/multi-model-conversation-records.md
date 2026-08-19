---
title: multi-model conversation records
state: researching
created: 2026-08-17
tags: [enhancement]
subsystems: [frontend]
milestone: v0.1.x
---

the context meter's usage record is last-wins, keyed by the model it was measured against; a conversation that bounces between models shows the last model's number or `?` until that model next reports. a per-model record map on the conversation object is the upgrade path — same shape, one entry per model — if anyone actually lives in that pattern.

## why

Deferred by the context usage tracking spec (built 2026-08, journal 2026-08-17): the map earns its storage and the meter's extra rules only once the bounce pattern shows up in real use.