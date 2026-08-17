---
title: tool badge warm tint
state: horizon
created: 2026-08-17
tags: [enhancement]
subsystems: [frontend]
---

tint the tools glyph's count badge warm (the family's warn color, reef's job-badge pattern) while any MCP server reports error state, so the bar signals a dead server without the reader opening the tool panel. the badge's meaning stays one number; the tint carries the failure.

## why

the metawoo toolbar arc (built 2026-08-17, journal 2026-08-17) shipped the badge as a pure count so its meaning stays one number. the family tints a badge warm when failures are present; pane's server error state is the signal that tint wants to carry.