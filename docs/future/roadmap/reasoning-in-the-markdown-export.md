---
title: reasoning in the markdown export
state: researching
created: 2026-08-16
tags: [feature]
subsystems: [frontend]
milestone: v0.1.x
---

Include a message's reasoning text in the markdown export. The export reads only `message.content`, so thinking is skipped by construction; including it (perhaps behind an option or in a collapsible section of the export) is a one-line-class change when wanted.

## why

The thinking display arc (built 2026-08, journal 2026-08-16) skipped it on purpose: the reasoning is background to the message, not content of it — a reader who wants the thinking has the conversation itself.