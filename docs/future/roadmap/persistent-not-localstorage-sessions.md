---
title: persistent (not localStorage) sessions
state: building
created: 2026-08-17
tags: [epic]
milestone: v0.1.x
---

it would be helpful if we could store sessions persistently on disk, instead of in `localStorage`. perhaps somewhere like `~/.local/share/pane` or similar (should be configurable).

when the iteration lands, the per-conversation usage record (context meter, built 2026-08-17) moves with the conversation: it is self-contained data on the conversation object, so it serializes with the history it describes and needs no re-derivation.
