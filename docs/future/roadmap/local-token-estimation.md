---
title: local token estimation
state: horizon
created: 2026-08-17
tags: [feature]
subsystems: [backend]
---

a local fallback that estimates context size for zero-usage responses. pi carries one because its compaction gate must fire even when the upstream goes quiet. pane has no gate today, so `?` is the meter's honest answer and the estimator stays out; it lands exactly when the compaction gate is scoped, and the two ship together — see auto compaction.

## why

Deferred by the context usage tracking spec (built 2026-08, journal 2026-08-17): local estimation re-opens the hole the arc's design closes — the upstream's `prompt_tokens` is measured with the tokenizer that actually owns the window, which is the whole point of the re-send property. an estimator is earned only by a gate that needs a number when the upstream reports nothing.