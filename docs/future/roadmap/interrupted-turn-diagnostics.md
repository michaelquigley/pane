---
title: interrupted turn diagnostics
state: inbox
created: 2026-09-29
tags: [enhancement, spike]
---

inspect how pane explains interrupted turns to the operator, including after reload, and which evidence subsequently reaches the model. distinguish a reported provider failure, a lost stream, an active or interrupted tool, and an uncertain tool outcome only when the existing evidence supports it. improve presentation of the current turn record rather than inventing another recovery system or adding automatic retries.

## first candidate

`RecoveryPanel` displays the saved `error_code` for retryable interruptions but omits it from the reconciliation branch. consider showing that code there too, with a focused render/reload test and no changes to execution certainty, reconciliation requirements, or provider history. a code is diagnostic evidence, not proof of the underlying cause or whether a tool ran.

## why

an interrupted conversation can leave the operator and model with different diagnostic information. a retry or server restart alone does not establish the cause or prove a recovery defect. surface the evidence pane already has while preserving uncertainty about side effects. coordinate with the existing [connection indicator](connection-indicator-on-the-toolbar.md) and [stream/approval signals](toolbar-stream-and-approval-signals.md) cards rather than duplicating their reachability and active-turn surfaces.
