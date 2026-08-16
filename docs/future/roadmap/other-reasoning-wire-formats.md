---
title: other reasoning wire formats
state: horizon
created: 2026-08-16
tags: [enhancement, spike]
subsystems: [backend]
---

Extend the upstream stream reader beyond the two tolerated delta spellings — `reasoning` (openai o-style) and `reasoning_content` (the vllm / sglang family; a 2026-08 capture against ninfer with a qwen3 model confirmed the latter is what it puts on the wire). Structured reasoning blocks and vendor-specific shapes are a design question, not a parser line: first look at what a backend a reader actually uses emits, then decide the tolerance shape.

## why

The thinking display arc (built 2026-08, journal 2026-08-16) kept the tolerance list deliberately short. A third shape on the wire is the revisit condition recorded in the arc's seam census.