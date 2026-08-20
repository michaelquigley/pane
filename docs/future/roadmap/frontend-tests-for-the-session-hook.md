---
title: frontend tests for the session hook
state: horizon
created: 2026-08-19
tags: [enhancement]
subsystems: [frontend]
milestone: v0.1.x
---

the persistent-sessions arc settled the session hook's ordering guarantees against a manual pass, and the ui has no frontend test runner. build it: a runner that fits the existing vite setup, a fake `SessionStore` with deferred promises, and focused tests for the invariants a refactor is most likely to break silently — the serial chain runs mutations in invocation order, so a `remove` enqueued after a save cannot execute ahead of it and re-create the file it just deleted; the diff-mirror runs exactly once per `setConversations` call and at call time, saving only added or reference-changed documents; hydration flips `loading` only when the working copy is fully installed, and a failed `list` never flips it; and the app's commit guard issues no write while chat's (messages, usage) are still the references the loaded document holds.

## why

the arc's reviews raised this as an advisory: the hook's invariants are the most regression-prone surface in the arc, and nothing in CI protects them. the operator declined test infrastructure inside the arc — a supervised manual pass suffices for a pre-1.0 build — and parked the work here. the server-assigned-session-versioning arc's 409 work is a natural trigger: it will touch the same chain and error-settlement machinery and will need the tests anyway.

this card was first written against the arc's earlier store-as-source-of-truth design, and rewritten 2026-08-19 when the mirror-model ruling retired the operations queue, the tail-only coalescing, and the transition token it originally named. none of that machinery was built; the guarantees above are what actually landed.
