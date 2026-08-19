---
title: server-assigned session versioning
state: researching
created: 2026-08-18
tags: [enhancement]
subsystems: [backend, frontend]
milestone: v0.1.x
---

the persistent-sessions arc settled last-write-wins as an accepted residual, and its cross-client half is a named hazard: a save built from a stale read can clobber a change another tab or machine made in the gap between the read and the write. the mechanism that closes it is a version the store assigns: the store increments a per-conversation counter on every accepted save, the document carries it, a save compares the version the caller holds and rejects a stale one with 409, and the frontend's error line becomes a conflict line the reader resolves — re-read or discard, while holding in-flight work. a client-side timestamp cannot do this job: a wall clock is not monotonic across tabs, so only a counter the store itself assigns is a true per-conversation order. this deepens the store's opaque-document seam — the version is the one field the store parses and manages on save — so that call belongs in a design session, not a work order.

build it when the residual's hazard stops being theoretical: two tabs or machines editing the same conversation in real use. until then the shipped design's snapshot guard suppresses the worst of the case (a read is not a write), and last-write-wins stands.

## why

the persistent-sessions spec (built 2026-08, journal 2026-08-18) parks this on its deferred menu: in the arc's design session the operator weighed building the 409 path against the appliance's one-human posture and chose to park it, and the arc's round-2 review (mercurius session s_D4PFZHu8r0v4) raised the reader-clobber case that points at exactly this mechanism. the snapshot guard closes the within-client half; this card owns the cross-client one.