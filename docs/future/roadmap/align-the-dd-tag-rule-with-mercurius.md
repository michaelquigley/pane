---
title: align the dd tag rule with mercurius
state: inbox
created: 2026-11-17
tags: [enhancement]
source: mercurius
---

pane's dd tag rule (`AGENTS.md` rule 8) and its terminus quality `projects/pane/dd-tag-minimalism` both say a `dd` tag is only for `+required`, `+extra`, or a name override, and the quality only looks at `internal/config`. mercurius rewrote its copy of that quality on 2026-11-17: a tag belongs wherever dd's default would be wrong, which admits `+omitempty`, `+nullable`, and `+opaque` with a stated reason for each, across every dd-bound struct. bring pane's rule and quality to the same statement, or write down why pane's should differ, so the two can become one `golang-conventions` quality. the rule and `AGENTS.md` are pane's to change; the canon edit is a proposal for michael, not something to make.

## why

the two projects started with the same quality, and `terminus-canon` has a card, `promote-dd-tag-minimalism-to-golang-conventions`, to promote it. that only works while they say the same thing. they no longer do, and pane's own code is further from its stated rule than mercurius's ever was.

## background

counted in pane's go code outside tests and `spike/`, 2026-11-17:

- 131 fields carry an explicit `dd` name. 126 of them restate what dd's conversion already gives (`Provider` tagged `provider`, `ID` tagged `id`); 5 are real overrides (`CreatedAt` as `createdAt`, `Version` as `v`, `AccountID` as `chatgpt_account_id`). the restating names are in `internal/sse/writer.go` (60), `internal/llm/types.go` (37), `internal/llm/round.go` (19), `internal/mcp/tool.go` (8), and `internal/api/approve.go` (2). the count uses a reimplementation of dd's snake-case rule, so check a few against dd itself.
- 78 fields carry `+omitempty` and 51 carry `+nullable`, neither of which rule 8 allows. 13 carry `+extra` and 1 carries `+required`.
- almost none of this is in `internal/config`, which is the only place the quality looks, so terminus has never raised it.

there is a real decision inside this, and it is pane's. the restating names are on wire structs, where an explicit name pins the contract against a go rename. if that is why they are there, the rule should say wire structs name their keys on purpose, and the quality should stop treating a restated name as noise in those packages. if it is habit, they come off. mercurius chose the second for its own structs; that does not settle it for pane, whose structs model other parties' formats.
