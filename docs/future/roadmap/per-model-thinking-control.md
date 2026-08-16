---
title: per-model thinking control
state: horizon
created: 2026-08-16
tags: [feature]
subsystems: [backend]
---

Let the reader enable or disable model thinking. Forcing thinking on or off means model-specific request extras (`enable_thinking`, chat-template knobs) plus a config surface, and knowledge of what each backend accepts — pane talks to ninfer directly, with no gateway layer such extras would be configured on.

## why

The thinking display arc (built 2026-08, journal 2026-08-16) deliberately shipped no control: whether the model thinks is what the model does. Until a model a reader actually uses defaults thinking off and they want it on, the knob has no user.