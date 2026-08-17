---
title: connection indicator on the toolbar
state: horizon
created: 2026-08-17
tags: [enhancement]
subsystems: [frontend]
---

a signal in the toolbar's signal column showing server reachability, flo's ping pattern: poll `/api/health` every 5s (2s while down), a warn dot and "server unreachable" while down. the column is shaped to take it. the ping hook belongs in `hooks/`, not the toolbar — the bar is presentation-only, and a timer is the seam condition its design session recorded: when the toolbar starts owning a timer, the hook lives elsewhere.

## why

the metawoo toolbar arc (built 2026-08-17, journal 2026-08-17) parked this on its deferred menu: pane's local single-user posture made a connection indicator lower value while the bar hosted no other live signal. it is the first deferred item that earns a timer on the bar's seam.