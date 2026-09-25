# openai subscription models: verification spike

date: 2026-09-23. scope accepted for a bounded protocol investigation; live login and generation require the separate approval below. this is a prototype handoff, not the production implementation work order.

## assignment

bootstrap from pane's `AGENTS.md`, recent `docs/journal/` entries, and [the design](openai-subscription-models.md). build an isolated go protocol harness and return evidence sufficient to finalize the adapter, continuation, and recovery schemas. do not implement the feature in pane's normal chat path or reopen accepted behavioral decisions. surface contradictions rather than designing around them silently.

the exact subscription targets are 'gpt-5.6-sol' and 'gpt-6-astra'. qwen targets are the existing 'qwen3.8-27b' connections on eleven (ninfer) and fortyfive (llama.cpp). do not substitute models or inference engines when a target fails.

## boundaries

- keep prototype code outside production packages, in an explicitly identified isolated directory. leave production configuration, conversation storage, frontend, and service processes unchanged. use the existing go/module conventions; no new llm sdk or general provider framework.
- use synthetic conversations and a local stdio mcp fixture with one deterministic harmless tool, such as adding two integers. the harness owns approval and execution; provider adapters never execute tools. assert that denied or incomplete calls produce zero executions.
- obtain subscription credentials through a separate operator-approved sign-in into a private scratch store. never read, copy, rotate, or log pi/codex credentials; never overwrite pane's eventual production auth store. keep tokens out of command arguments, fixtures, reports, and repository files.
- no account switching, live quota exhaustion, production mcp tools, remote writes, deployment changes, commits, or pushes. no transparent retries or automatic budget increases.
- output caps, context capacity, and account access must be established for the actual route, not borrowed from a public api model page or inferred from one successful answer.

## stage 1: source pinning and offline harness

record the exact inspected pi package version/source revision or file hashes; the earlier local inspection was version '0.85.1', not a promise about the current installation. pin the auth, refresh, request-building, streaming, and replay code used as evidence. consult current official documentation where applicable and distinguish it from backend behavior inferred from pi. resolve the concrete oauth client, destinations, scopes, and callback/device flow before asking the operator to sign in.

read the qwen evidence already supplied:

- `/home/michael/Sandbox/qwen-reasoning-controls.md`: eleven's source/log investigation.
- `/home/michael/Sandbox/qwen38/qwen3.8-request-contract.md`: fortyfive's original source contract.
- `/home/michael/Sandbox/qwen3.8-request-contract.md`: section-9 supplement with 16 completed prompt-only checks, not the full contract.
- `/home/michael/Sandbox/qwen38/qwen3.8-chat-template.jinja` and `contract/out/`: template and synthetic prompt-test artifacts.
- `/home/michael/Sandbox/qwen38/qwen3.8-fortyfive-sizing.md`: deployment history; the later correction establishes one slot and a 163,840-token per-request ceiling. do not use the superseded two-slot recommendation.

build local tests for request conversion, normalized stream events, complete-round boundaries, usage/finish metadata, tool-call/result pairing, ordered provider items, and credential-destination isolation. test identity changes, legacy history without metadata, and serializing/reloading continuation without a live provider.

inject stream truncation, cancellation, malformed or incomplete tool arguments, an observed tool result followed by disconnect, and dispatch with unknown outcome. prove the harness does not rerun tools to recover. mock refresh rotation, revoked login, and allowance errors rather than provoking them on a real account. mock success is evidence of local handling only.

propose concrete continuation-envelope and interrupted-turn records against these cases. keep qwen reasoning request-local; do not require blanket historical-thinking replay or add a durable execution ledger.

## stage 2: approval before live work

stop and give michael a compact run plan naming the executing agent/host, exact endpoints/models, login flow, private credential location, synthetic prompts/tool, and maximum upstream requests. count each generation round and any retry separately. also specify per-request and total wall-clock ceilings, qwen output caps, and any verified subscription output control. if subscription output cannot be capped, say so; a local timeout is cancellation best-effort, not a guaranteed upstream token limit.

coordinate qwen requests with the endpoint owners; fortyfive serializes generation and probes can delay real work. the existing prompt-only approval does not authorize generation. wait for approval before sign-in, authenticated live probes, or inference. stop if the approved ceiling is exhausted; report inconclusive cases rather than extending the run.

## stage 3: authorized protocol exercises

use the approved request budget to cover the smallest useful matrix. unsupported or budget-limited cases remain explicitly unknown.

| exercise | evidence required |
|---|---|
| subscription sign-in and streaming | each exact model returns a streamed answer; capture sanitized event/usage/finish shapes and observed effort handling; identify the login flow actually tested |
| tool loop | model requests the harmless tool, harness approves and executes it once, then sends the result and receives a final answer; include denial through mocks or the approved live budget |
| durable continuation | save a completed assistant round with required continuation, terminate the harness, reload in a fresh process, and continue using full history; no hidden in-memory provider session |
| switching | synthetic qwen → sol → astra → sol sequence with portable answers/tool history preserved and opaque replay filtered; record which qwen endpoint was tested |
| qwen controls | non-default effort and disabled thinking on each engine; distinguish accepted request fields, effective-setting evidence, and observed outputs rather than claiming a quality comparison |
| qwen tool recovery | active-round reasoning replay, completed-turn omission, and forced-final instruction in the initial system block with tools omitted; prove response parsing and completion, not merely rendering |

do not repeat fortyfive's passed prompt-rendering matrix. do not test hard reasoning budgets, benchmark context limits, or deliberately cause side effects. browser/device login alternatives or natural token refresh that cannot be observed in the budget remain source/mock-verified or unknown, not implicitly passed.

## stop conditions and deliverables

stop on credential-destination mismatch, credential exposure risk, a protocol requirement contradicting an accepted decision, unresolved execution outcome, or the agreed time/request limit. do not bypass the failing case by changing models, auth stores, billing routes, or infrastructure.

return a report alongside this brief as `openai-subscription-models-spike-results.md`, containing:

- a result matrix labeled live-verified, prompt-verified, source-established, mock-tested, failed, or unknown; include actual request counts and versions.
- proposed adapter/event, continuation, provenance, and recovery schemas, with assumptions and remaining blockers named.
- reproducible commands and sanitized fixtures that the implementation can turn into regression tests. remove authorization headers, credentials, and account identifiers; keep real account-bound continuation private, using structural synthetic fixtures in the repository.
- the disposition of prototype files and scratch credentials. remove only known generated build artifacts; do not delete credentials or captures without operator direction, and identify anything private left behind without revealing its contents.

the planning agent incorporates the evidence, develops `openai-subscription-models-work-order.md` in numbered stages, and takes the spec and work order through mercurius before production implementation. the spike is finished when its approved questions have results or explicit blockers, not when the whole feature is built.
