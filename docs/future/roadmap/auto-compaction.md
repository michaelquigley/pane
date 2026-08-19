---
title: auto compaction
state: researching
created: 2026-08-17
tags: [epic]
subsystems: [backend, frontend]
milestone: v0.1.x
---

the backend→frontend contract that lets pane replace stored conversation history when the context window fills. pane's browser-owned history means the backend cannot rewrite the browser's state on the user's behalf: compaction needs a protocol in which the frontend accepts a replacement history and re-stores it. the context usage arc landed the two facts a compaction gate will want — raw per-round usage on the wire and the operator's windows in config — and the usage record rides the conversation object, so a compaction result can ride with it. the gate's zero-usage fallback is its sibling card, local token estimation.

**note:** maybe we store the pre-compaction conversation and append the compacted version? or otherwise keep prior-to-compaction conversation. the client knows not to re-send the old versions, but it's there for analyzing agent and model behavior.

## why

Deferred by the context usage tracking spec (built 2026-08, journal 2026-08-17): the gate is out of scope of the meter, and its plumbing is deliberately necessary-but-not-sufficient. the seam census's wire-purity call survives the gate: it computes from usage plus config without any protocol change.