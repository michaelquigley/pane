---
title: toolbar screen-reader announcer
state: researching
created: 2026-08-17
tags: [enhancement]
subsystems: [frontend]
milestone: v0.1.x
---

a polite live region in the toolbar announcing turn and tool-approval transitions (flo's JobAnnouncer pattern): turn started, a tool call awaiting approval, round complete, turn failed. always mounted because the toolbar never unmounts; the visible state already rides the badge and the message flow, so this renders the screen-reader channel only.

## why

the metawoo toolbar arc (built 2026-08-17, journal 2026-08-17) shipped the tools count in the glyph's dynamic label — one number reaching assistive tech exactly once — and parked the broader announcer: the chat is the live region, and a second one in the toolbar is an accessibility decision, not a chrome decision.