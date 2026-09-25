# openai subscription models: spike results

date: 2026-09-23. status: **stages 1–3 complete.** michael approved the run plan on 2026-09-23, with originator 'pane' and the qwen key read from `~/.config/pane/config.yaml`, and both hosts were available. the run used 21 of 23 approved upstream requests over about 2 minutes 10 seconds (sol 9, astra 2, eleven 5, fortyfive 5, auth 0). there were no retries, and no failure beyond S6's intended probe. one core question remained unknown. a separately authorized, subscription-only follow-up the same afternoon then **passed live encrypted-reasoning replay on both models** (6 of 6 requests; see 'encrypted-reasoning replay follow-up').

executing agent: claude code (opus 5.5), on host 'fortyfive'. prototype: `spike/openai-subscription/`, its own go module and outside pane's production packages. production config, storage, frontend, and `pane.service` are unchanged.

## source pinning

the installed pi is **'0.87.1'** (`@earendil-works/pi-coding-agent` and `@earendil-works/pi-ai`), installed 2026-09-23 10:57 local time, not the '0.85.1' inspected earlier. evidence below cites these file hashes under `…/pi-coding-agent/node_modules/@earendil-works/pi-ai/dist/`:

| file | role | sha256 |
|---|---|---|
| `auth/oauth/openai-codex.js` | oauth client, browser + device flows, refresh | `41432753ef2ec7ef21c577fdc8f83b731c78be90c10ed8e7e1df7010f84d198f` |
| `auth/oauth/device-code.js` | device polling loop | `8f197cc9af67be64b82939573d719d1b55388f96f2e53cb8c47f9618fc1297eb` |
| `auth/oauth/pkce.js` | pkce | `d54668654e89d6fe6994a09a7f9399e732a411fcb31c99eb8b5e60349763708c` |
| `auth/resolve.js` | double-checked refresh under lock | `82ee45ecec319f59536759312a4de25313a8bb8cb7ce43db43d18edc10fef305` |
| `api/openai-codex-responses.js` | request builder, SSE/websocket transport, errors | `6e69310d77278231cfc87d7f03ee815d4a0f2ff273e6c43fcee6835e7df2b0c7` |
| `api/openai-responses-shared.js` | message conversion, stream processing | `7846279b34c2a569bda2b0753c8b083f8b976204b4fdd7586095ebbb6a643410` |
| `api/transform-messages.js` | cross-model filtering, orphan handling | `9d747a3d64c533f7bfaf2a66e8446dc006086559d364a09d4c51d1c8c9c332e5` |
| `providers/openai-codex.js` | base url | `769490a94a5bcb062c22e768cf0621c91fb1c5d6be3565e6a6fa765eeeabe674` |
| `providers/data/openai-codex.json` | model catalog, effort maps | `4bb30a26d1b40e1f67c9f24891fca0ce25b030bc4cbbb529be78608cd4466fdf` |

the codex cli '0.155.1' binary on this host contains the same oauth client id and the originator string 'codex_cli_rs'. that is string evidence only; source is not available here.

**official documentation** ([chatgpt auth](https://learn.chatgpt.com/docs/auth)) confirms browser and device-code sign-in for codex clients, tokens cached in `~/.codex/auth.json`, and automatic refresh. [codex for open source](https://developers.openai.com/community/codex-for-oss) names pi among the tools developers may use. neither page documents the backend endpoint, the oauth client, request fields, or stream shapes: **everything about the wire contract below is inferred from pi, not from a public contract.**

### resolved oauth and destinations (source-established, pi 0.87.1)

| item | value |
|---|---|
| client id | `app_EMoamEEZ73f0CkXaXp7hrann` (the codex cli's public client; pi borrows it) |
| scopes | `openid profile email offline_access` |
| browser flow | authorization code + PKCE S256 at `https://auth.openai.com/oauth/authorize`, loopback callback `http://localhost:1455/auth/callback` (port fixed by the registered redirect), extra params `id_token_add_organizations=true`, `codex_cli_simplified_flow=true`, `originator=<client>` |
| device flow | POST `…/api/accounts/deviceauth/usercode` → user visits `https://auth.openai.com/codex/device` → poll `…/deviceauth/token` (403/404 = pending) → exchange with redirect `https://auth.openai.com/deviceauth/callback` |
| token / refresh | `https://auth.openai.com/oauth/token`, form-encoded, refresh rotates the refresh token |
| account binding | jwt claim `https://api.openai.com/auth`.`chatgpt_account_id`, sent as header `chatgpt-account-id` |
| generation | POST `https://chatgpt.com/backend-api/codex/responses`, SSE, `OpenAI-Beta: responses=experimental`, `originator`, bearer |

## result matrix

labels: live-verified, prompt-verified, source-established, mock-tested, failed, unknown. "mock-tested" means the harness's local handling passed against synthetic upstreams, not provider behavior.

| question | label | evidence / note |
|---|---|---|
| oauth client, flows, destinations | live-verified (device flow), source-established (browser) | pane's own device-code sign-in into the scratch store succeeded; access token valid ~10 days; browser/PKCE flow not exercised |
| pane-owned sign-in into a private store, never touching pi/codex/pane production stores | mock-tested | `auth` tests: forbidden-path refusal, 0700/0600, redaction |
| refresh rotation, single refresh across concurrent processes | mock-tested | `TestRefreshRotationSingleFlight` (8 callers, 2 managers, 1 refresh, rotated token persisted) |
| revoked login → login required, credentials kept | mock-tested | 400 from token endpoint; file untouched; token not echoed |
| transient refresh failure preserves credentials | mock-tested | 503 then recovery |
| refresh returning a different account is refused | mock-tested | new design guard, not in pi |
| natural token refresh on the real route | unknown | token lifetime ~10 days; no refresh occurred (auth route 0 of 2) |
| credential destination isolation (host, path, query, redirect) | mock-tested | `TestDestinationIsolation`, `TestAuthClientDestinations`; a foreign server receives zero requests and no bearer |
| each model streams an answer; event/usage/finish shapes | live-verified | S1 sol, S2 astra: `completed`, answer 'ready'; originator 'pane' accepted. sanitized live shape in `testdata/codex/live-shape-toolcall.sse` |
| effort handling per model | live-verified ('low' on both), source-established (rest) | terminal response echoes `reasoning: {effort: low, summary: detailed, mode: standard, context: all_turns}` for both models. other levels come from pi's catalog; astra cannot be disabled |
| subscription output cap (`max_output_tokens`) | live-verified: **unsupported** | S6: HTTP 400 `Unsupported parameter: max_output_tokens`, classified `invalid_request`, not retried. responses echo `max_output_tokens: null` |
| context window for the route | unknown | pi catalog says 272000 / 128000 max tokens for both: a catalog claim, not route evidence; not probed (benchmarking context is out of scope) |
| request conversion, ordered items, call/result pairing | mock-tested | `codex` and `qwen` tests |
| complete-round boundary; truncation, malformed args, incomplete, failed, cancel never executable | mock-tested | `TestFaultsNeverYieldExecutableRounds`, `TestCancellationMidStream`, qwen `TestStreamFaults`, loop `TestIncompleteRoundsNeverDispatch` (0 executions) |
| usage/finish normalization | mock-tested | responses usage incl. cached/reasoning tokens; chat usage chunk |
| identity change filters replay; stored record untouched | mock-tested | model change, account change, tampered envelopes (10 mutations) all fall back to portable history |
| legacy history without metadata | mock-tested | portable conversion, synthetic `msg_pane_N` ids, no provider item ids |
| serialize/reload continuation, fresh process, no hidden session | live-verified | S4 (function-call items) and the follow-up (encrypted reasoning, both models): stored byte-equal to the stream, selected as compatible, transmitted byte-equal and contiguously with its companion `message` item, accepted by the backend. same-round reasoning + `function_call` pairing remains unknown |
| qwen → sol → astra → sol switching | live-verified | S5 on Q3's fortyfive document: each model answered '42' from portable history; the final sol request replayed sol's earlier message item (real id and phase) and was accepted; astra received it portably |
| tool loop with harness approval/execution; denial | live-verified (approval), mock-tested (denial) | S3 sol, Q3 eleven and fortyfive: one `add(17, 25)` call, one execution each, final '42' |
| observed result then disconnect → no rerun | mock-tested | 1 execution total; recovery = continue as new turn |
| dispatch with unknown outcome → reconciliation, no rerun | mock-tested | fixture acts then result is lost; 1 execution; blocked until reconciled |
| dispatch mark persisted before execution | mock-tested | `TestDispatchMarkPersistedBeforeCall` reads the file inside the hook |
| allowance / rate limit / auth / access errors classified | mock-tested | 429 `usage_limit_reached`, 429 rate limit, 401, 403, `response.failed`; never retried |
| qwen effort/none request fields (both engines) | source-established (eleven), prompt-verified (fortyfive) | owner reports; harness emits only top-level `reasoning_effort`, never `enable_thinking` |
| qwen non-default effort and disabled thinking, generation | live-verified | Q1 'low': thinking then 'ready' on both (52 prompt tokens). Q2 'none': no thinking deltas, 'ready', 28 prompt tokens (no effort instruction), 2 completion tokens |
| qwen effective sampling | unknown | neither engine echoes sampling in the stream (timings only). fortyfive accepted the overrides with HTTP 200; eleven's resolved settings need its request log |
| qwen active-round reasoning replay | live-verified | Q3: tool round `finish_reason: tool_calls` on **both** engines; the next round reused 415/433 (eleven) and 414/430 (fortyfive) prompt tokens from cache. the replayed `reasoning_content` re-rendered the generated prefix exactly. nothing was persisted |
| qwen forced-final in initial system, tools omitted | live-verified | Q4 on both engines at default effort: seeded failed-tool history, no tools requested, final '42' |
| codex forced-final (tools omitted with function-call history) | live-verified | S7: accepted with no `tools` field and portable function-call history; final '42' |
| empty instructions (pane 'none' system prompt mode) | live-verified | S8: `instructions: ""` accepted (echoed as null); answer 'ready' |

request counts: 21 upstream (sol 9, astra 2, qwen-eleven 5, qwen-fortyfive 5, auth 0) against a ceiling of 23. versions: pi/pi-ai '0.87.1', codex cli '0.155.1' (strings only), go '1.27.1' toolchain with module `go 1.26`, mcp-go 'v0.43.2', ninfer 'a140e7ae' and llama.cpp '0adcc3b'/'b10502' per owner reports.

## findings that bear on accepted decisions

1. **pi converts cross-model thinking into assistant text** (`transform-messages.js`: non-same-model thinking becomes `{type: "text"}`). this contradicts the accepted rule that displayed thinking is never converted. pane must not port that behavior; the harness drops foreign reasoning.
2. **pi manufactures `"No result provided"` error results for orphaned calls and silently drops errored/aborted assistant messages.** the accepted policy forbids fabricated successes. pi's placeholder is an error, but it is still invented. the harness refuses to send orphaned calls (a local error before any request) and emits honest outcome notes from the recovery record instead.
3. **pi's default transport is websocket with connection-scoped `previous_response_id`**, which is a hidden in-memory provider session. the harness uses only SSE with full history and `store=false`. pi's own comment records that the backend rejects `store: true`.
4. **pi sends effort 'none' for sol when no level is chosen** (its `off` default). the accepted rule "omission preserves upstream default" therefore differs from pi's observed practice. the harness omits `reasoning` entirely, and whether that yields reasoning summaries for display is unknown.
5. **no pane-registered oauth client exists.** pane's "own sign-in" necessarily uses the codex cli's public client id, as pi does. the design already accepts pi-style practice. the remaining choice is the `originator` value, below.
6. **pane's current loop can execute calls from a truncated round.** `RunToolLoop` checks `[DONE]` but not `finish_reason`, so a `length`-finished round with one complete call would dispatch it. the harness makes only terminal, validated, tool-requesting rounds executable. production pane is unchanged; this belongs in the work order.
7. **pane emits `tool_call_executing` before argument parsing**, so the browser's "executing" signal does not mean "dispatched". the recovery record needs its own dispatch mark, written before the executor runs, as the harness does.
8. **the qwen non-thinking sampling presets still differ:** ninfer's automatic preset includes `presence_penalty 1.5`, while the llama.cpp profile overrides only temperature/top_p/top_k (0.7/0.8/20). this is unresolved and not decided here.

## proposed schemas

the code is the precise proposal: `spike/openai-subscription/round/round.go` (adapter/event/round), `codex/requestBuilder.go` (envelope validation), and `loop/recovery.go` (recovery). summary:

**adapter and events.** `Adapter.Round(ctx, Request{History, Tools, Intent, Local, CacheKey}, emit) → (*Final, error)`, one upstream round per call. progressive events are `text_delta`, `thinking_delta`, `tool_call_start`, `tool_call_args`, `tool_call_done`, and all of them are display-only. a `Final` exists only after a terminal event and carries the portable assistant message (with `origin` and optional `continuation`), a normalized `finish` (`stop`, `tool_calls`), the raw finish, usage (input, cached, output, reasoning, total), and request-local data. `Final.Executable()` is true only for `finish=tool_calls` with validated calls. everything else is a classified `round.Error` (transport, truncated, protocol, cancelled, auth, allowance, rate_limited, model_access, invalid_request, upstream, incomplete, budget, destination) carrying a non-executable `Partial`.

**provenance** (per assistant message):

```json
{"origin": {"alias": "gpt-5.6-sol", "effort": "low",
  "identity": {"provider": "openai-codex", "protocol": "responses", "upstream_model": "gpt-5.6-sol",
               "service": "chatgpt.com/backend-api/codex", "account_scope": "chatgpt:<24 hex>"}}}
```

`account_scope` is a salted sha256 of `chatgpt_account_id`, so it survives token rotation and holds no credential bytes. qwen identities carry `service` (endpoint origin) and `profile` instead. alias and effort are provenance only and do not gate replay.

**continuation envelope** (per assistant round, opaque to the browser):

```json
{"continuation": {"v": 1, "format": "codex-responses-items", "identity": {…same as origin…},
  "items": [{"type": "reasoning", "id": "rs_…", "encrypted_content": "…"},
            {"type": "function_call", "id": "fc_…", "call_id": "call_…", "name": "add", "arguments": "{…}"}],
  "bindings": [{"call_id": "pane_call_…", "provider_call_id": "call_…", "provider_item_id": "fc_…"}]}}
```

replay requires exact identity equality, a supported version and format, origin agreeing with the envelope, allowed item types only, no trailing reasoning item, output text equal to the portable content, and a one-to-one match between bindings, provider items, and portable calls (name, arguments, item id). any failure excludes the envelope and uses portable conversion. the stored message is never altered. portable tool-call ids stay pane-owned; outputs pair with whichever call id was actually sent. qwen writes no envelope: its active-round reasoning is `Local`, keyed by history index, replayed only to the same connection after the last user message, and never persisted.

**recovery record** (per submitted request, persisted incrementally beside the messages):

```json
{"v": 1, "state": "interrupted", "origin": {…}, "history_len": 2,
 "rounds": [{"assistant_index": 2, "finish": "tool_calls", "results_committed": false,
   "calls": [{"call": {…}, "state": "completed", "result": "42"},
             {"call": {…}, "state": "unknown", "reconciled": {"executed": "yes"}}]}],
 "reason": "cancelled", "error_kind": "cancelled", "partial_text": "…", "preview_calls": […]}
```

call states: `pending`, `not_dispatched`, `denied`, `rejected`, `dispatched`, `completed`, `failed`, `unknown`. `dispatched` is persisted before the executor runs, and an interruption turns a lingering `dispatched` into `unknown`. `Assess` returns `retry_from_scratch` only when no call reached the executor, `reconcile` while any unknown is unresolved, and otherwise `continue_as_new_turn`. `PortableContinuation` gives every committed call an honest result: the recorded result, "not executed…", or the reconciled statement. it never produces a fabricated success. partial text is display-only and never sent as an answer.

**assumptions.** the harness's recorder is a synced file. pane's browser mirror is not crash-safe, so the production record inherits the accepted "no exactly-once claim" limits. `MaxEnvelopeBytes` (512 KiB) is a placeholder; pane's document cap is 32 MiB.

## encrypted-reasoning replay follow-up

authorized separately by the design agent, relayed by michael, 2026-09-23. it used subscription models only, effort 'medium', and the existing scratch login, with no qwen key use and no refresh. it had its own ledger (`budget-followup.json`: sol 3, astra 3, auth 0, 20 minutes); the first run's ledger was left unchanged. the prompt was a synthetic marble problem: use `add` once for 17 + 25, then reason about equal bags. the follow-up was "how many marbles would two such bags hold?" with tools withheld. `--approve-once` capped execution at one call per model. structural checks come from `spike/openai-subscription/scripts/replaycheck.py`, which prints counts, booleans, item types, and decisions only.

| check | sol | astra |
|---|---|---|
| requests (tool round, answer round, reload) | 3 | 3 |
| tool executions | 1 | 1 |
| tool-round envelope | `function_call` only | `function_call` only |
| answer-round envelope | `reasoning` (nonempty encrypted, 1484 chars) + `message` | `reasoning` (nonempty encrypted, 1356 chars) + `message` |
| stored encrypted content equals the streamed item | yes | yes |
| still equal after the fresh process re-saved the document | yes | yes |
| replay decisions in the reload process | both assistant rounds `compatible` | both assistant rounds `compatible` |
| reload request input order | user, function_call, function_call_output, **reasoning, message**, user | same |
| envelope items transmitted contiguously, in stored order | yes (both rounds) | yes (both rounds) |
| encrypted reasoning transmitted byte-equal | yes | yes |
| reload request: tools / store / include | none / false / `reasoning.encrypted_content` | same |
| backend outcome | `completed`, answer '84' | `completed`, answer '84' |
| verdict | **pass** | **pass** |

the follow-up used 6 of 6 requests in about 50 seconds, with no refresh, no retries, and no failed requests. sol displayed a reasoning summary; astra's reasoning item carried an empty summary, so no thinking text would be shown for it, yet its encrypted content was stored, replayed, and accepted. **requirement for implementation: encrypted continuation must be preserved and replayed independently of its display summary; an empty or absent summary is never a reason to drop or skip the item.** the synthetic `toolcall.sse` reasoning item (empty summary, encrypted content) keeps this under offline test. sol's reload round produced a further reasoning item, also stored equal; astra's did not.

## live-run observations

- **the route fixes several settings and echoes them.** requesting `summary: auto` comes back as `detailed`, and `text.verbosity` as `medium`. `temperature` is 1.0 and `top_p` 0.98. `truncation` is `disabled`, `prompt_cache_retention` is `24h`, and `reasoning.context` is `all_turns`.
- **terminal responses carry more than pi parses.** they include a `safety_identifier` string (account-derived; redact it from any capture), a `usage.attribution` map keyed by output item ids, `cache_write_tokens`, and an `obfuscation` field on text deltas. the harness ignores them. the captures keep them privately; the committed fixture redacts or drops them.
- **at effort 'low' on one-line prompts, neither model produced reasoning tokens or items**, so no thinking was displayed and no encrypted reasoning was stored. qwen at 'low' still thought.
- **both qwen engines report `finish_reason: tool_calls` on tool rounds and `stop` on answers**, and both send usage in a final empty-`choices` chunk with `timings`. only eleven reports `completion_tokens_details.reasoning_tokens`.
- **usage events are trustworthy on all three protocols** and include cached tokens. the subscription usage also carries `cache_write_tokens`.

## remaining blockers and unknowns

1. **replay of a reasoning item that precedes a `function_call` in the same round is live-unverified.** it is mock-tested only (`TestToolCallRoundAndEnvelopeReplay`, synthetic `rs` + `fc` round). in the follow-up, both models reasoned only in the answer round after the tool result; their tool-requesting rounds held a lone `function_call`. pi's comment says the backend tracks which `fc_` items were paired with `rs_` items. this pairing is the remaining untested continuation shape, most likely at higher effort or with a harder pre-tool decision.
2. **no subscription output cap exists.** explicit `max_tokens` on `openai-codex` entries must be rejected (already the accepted fallback rule). per-request cost can only be bounded by effort and timeouts.
3. **context capacity is unknown for the route.** pi's catalog value of 272000 is not evidence.
4. **qwen effective sampling is unknown**, and the preset discrepancy (finding 8) is unresolved.
5. **natural refresh, browser/PKCE login, and allowance exhaustion** are mock- or source-verified only.

## decisions taken for the live run

1. **originator value:** 'pane', as michael chose. the backend accepted it on every generation request. device login does not send an originator.
2. **qwen key:** copied once from `~/.config/pane/config.yaml` into the owner-only `~/.local/state/pane-spike/qwen.key`, never printed.
3. **coordination:** michael cleared both hosts for immediate use. eleven's request log has not been read for resolved effort and sampling.

## stage 2 run plan (approved and executed)

- **agent/host:** this claude code session on 'fortyfive'. binaries are built into `~/.local/state/pane-spike/bin/`.
- **login:** device code (`spike login --method device`). the agent runs it and relays the verification url and user code, and michael completes it in a browser. browser/PKCE is the fallback only if device auth is refused, and needs port 1455 free on this host. tokens are never printed.
- **private credential location:** `~/.local/state/pane-spike/auth/openai-auth.json` (0600, dir 0700). captures and documents live under `~/.local/state/pane-spike/{captures,docs}`.
- **endpoints/models:** `https://chatgpt.com/backend-api/codex/responses` with 'gpt-5.6-sol' and 'gpt-6-astra'; `http://eleven:11400/v1` (profile `qwen3.8-ninfer`) and `http://fortyfive:11400/v1` (profile `qwen3.8-llamacpp`), upstream 'qwen3.8-27b'.
- **tool:** `mcpadd` fixture (`add(a, b)`), executed only by the harness.

| id | route | exercise | synthetic prompt | requests |
|---|---|---|---|---|
| S1 | sol | streamed answer, effort low | "reply with exactly: ready" | 1 |
| S2 | astra | streamed answer, effort low | same | 1 |
| S3 | sol | tool loop, approved, 1 execution | "use the add tool to add 17 and 25, then reply with only the sum." | 2 |
| S4 | sol | fresh process reload + continue from full history | "now add 1 to that result without tools. reply with only the number." | 1 |
| S5 | sol, astra, sol | switching on Q3's fortyfive document | "what was the last sum? reply with only the number." | 3 (sol 2, astra 1) |
| S6 | sol | `max_output_tokens: 16` acceptance/enforcement probe | "count from 1 to 200 separated by spaces." | 1 |
| S7 | sol | forced-final round on the seeded failed-tool document | (seeded) | 1 |
| S8 | sol | empty `instructions` (pane 'none' mode) | "reply with exactly: ready" | 1 |
| Q1 | eleven, fortyfive | effort low answer | "reply with exactly: ready" | 1 + 1 |
| Q2 | eleven, fortyfive | effort none answer (fortyfive sends sampling overrides) | same | 1 + 1 |
| Q3 | eleven, fortyfive | tool loop at effort low with active-round reasoning replay | the S3 prompt | 2 + 2 |
| Q4 | eleven, fortyfive | forced-final recovery round on the seeded document | (seeded) | 1 + 1 |

- **ceilings:** sol 9, astra 2, qwen-eleven 5, qwen-fortyfive 5, auth (refresh only) 2. **total 23** upstream requests, counting every round and any failure. there are no retries. a failed case is reported, not repeated. denial stays mock-tested.
- **wall clock:** 3 minutes per request, 10 minutes per invocation, **60 minutes total** from the first spend, all enforced by the ledger.
- **qwen output caps:** `max_tokens` 4096 for thinking-enabled requests and 1024 for effort 'none'. fortyfive worst case is roughly 75 s per request at observed decode rates.
- **subscription output control:** none verified. S1–S5, S7, and S8 run uncapped at effort low with one-line prompts. the per-request timeout only cancels best-effort; it is **not** an upstream token limit. S6 is the only capped request and tests whether a cap exists.
- **stops:** the run stops on credential-destination mismatch, originator rejection, any protocol requirement contradicting an accepted decision, an unresolved execution outcome, or an exhausted ledger. the harness enforces the ledger and destinations; the rest is agent discipline.

## reproducible commands

offline (no network):

```bash
cd spike/openai-subscription && go vet ./... && go test -count=1 -race ./...
```

43 tests pass across the auth, budget, codex, e2e, loop, and qwen packages. synthetic structural fixtures are in `spike/openai-subscription/testdata/codex/`, and the qwen stream fixtures are inline in the tests. none contain credentials, account identifiers, or real continuation. live commands are in `spike/openai-subscription/README.md`.

## disposition

- prototype source `spike/openai-subscription/` is uncommitted, for review. it should not be merged into production packages; the work order can lift the schemas and tests. the only live-derived repository file is `testdata/codex/live-shape-toolcall.sse`: S3's first round with every id renamed, `safety_identifier`/`prompt_cache_key`/`obfuscation` redacted, and `attribution` removed. it contains no credential, account id, or encrypted content.
- build artifacts: the run binaries (`~/.local/state/pane-spike/bin/`) were removed after each run. tests build into temp directories and remove them.
- **live spike closed, 2026-09-23.** no further login, inference, refresh, or prompt-search requests are authorized. before cleanup, the repository's 41 spike and doc files were checked against every credential value, the account scope, all encrypted reasoning items, and the safety identifier. there were zero matches.
- **removed from `~/.local/state/pane-spike/`** (64 files and 2 emptied directories, each path resolved and checked as a regular file first):
  - the scratch subscription login (`auth/openai-auth.json` and its lock), removed locally only; nothing was revoked upstream.
  - the copied `qwen.key`.
  - all 44 capture files under `captures/`: 22 raw SSE captures, 22 per-run summaries (these held provider item ids), and 6 outgoing request bodies containing encrypted reasoning.
  - 9 subscription conversations with account scope and continuation, including the two follow-up documents with encrypted reasoning.
  - `login.out` and `login.err`.
  - the run binaries, which were already removed after each run.
- **recoverability:** the files were plain-unlinked (no trash, not securely shredded), so they are not recoverable by ordinary means. the upstream chatgpt login session was not revoked; its only refresh token lived in the deleted file. the grant lapses on its own or can be revoked from the account's security settings.
- **retained there, verified non-sensitive:**
  - `budget.json` and `budget-followup.json`, with their empty locks: routes, ceilings, counts, times, "generation" notes.
  - `fixture-exec.log`: four `add 17 25` lines, one per live tool execution.
  - `run-summaries.json`, a sanitized replacement for the deleted per-run summaries: connection, effort, finish, usage, tool counts, replay decisions, and event type names, without ids, identifiers, or payloads.
  - 8 qwen-only synthetic conversations (`docs/q1`–`q4`), which have no account binding and no continuation.
- **retained in the repository (uncommitted):** the prototype source, `scripts/replaycheck.py`, the tests, the synthetic and sanitized fixtures, and this report. production pane, pi, codex, and their credential stores were not touched.
