# openai subscription models

status: ready to build, updated 2026-09-24. mercurius round 3 returned 'ready_to_build' with no blocking findings; all three advisory clarifications were accepted and applied, and the review is closed. the paired work order supplies the implementation contracts and stages. the isolated spike verified encrypted-reasoning replay on both subscription models for reasoning-plus-message rounds; same-round reasoning-plus-tool-call replay remains live-unverified. production implementation has not started; this is not built pane behavior.

## intent

bring 'gpt-5.6-sol' and 'gpt-6-astra' into pane through the user's chatgpt/codex subscription. they participate in the same model registry and selector as the existing openai-compatible models. pane remains a single go binary with an embedded browser interface and owns the conversation's mcp tool execution, approvals, and results.

the implementation is a native provider, informed by pi's subscription integration. control over tools in the conversation is the reason to keep the model connection inside pane. the backend's chat path remains stateless: each request supplies its history, including any continuation data needed to reproduce that history for the selected provider.

## models and connections

the model registry continues to define the aliases available in the selector. `provider` selects transport and authentication; omission defaults to `openai-chat-completions`, preserving existing behavior. this names a compatible protocol, not the hosting company. `openai-codex` selects the subscription connection. the legacy configuration without a registry remains supported.

the accepted split is `provider`, `compatibility_profile`, and `reasoning_effort`. small built-in compatibility profiles own model/engine conventions: supported effort values, sampling adjustments, reasoning preservation, and recovery instruction placement. the initial qwen profiles are `qwen3.8-ninfer` and `qwen3.8-llamacpp`; both use `openai-chat-completions`. profiles are explicit, not inferred from aliases or hostnames, and do not provide arbitrary request-body overrides. existing entries without a profile retain their behavior. fixtures and deployment verification establish profile compatibility, not an assumption about every future engine release.

the accepted configuration shape is:

```yaml
models:
  gpt-5.6-sol:
    provider: openai-codex
    upstream_model: gpt-5.6-sol
  gpt-6-astra:
    provider: openai-codex
    upstream_model: gpt-6-astra
```

these entries share the openai account established by pane's login command. credentials belong to the provider; aliases and upstream model ids belong to the registry. listing a model does not establish that the signed-in account is entitled to use it. subscription context capacity remains unknown unless operator-configured; explicit subscription output caps are rejected because no supported mapping is established. configuration validation follows the accepted isolation rules below.

## reasoning controls

reasoning effort belongs in each model's registry entry in the first version. this includes thinking-level control for the existing qwen models, not only the new openai connections. aliases can select different presets for the same upstream model without adding a separate toolbar control.

the accepted configuration uses one `reasoning_effort` field, with 'none' meaning disabled only where supported. omission preserves upstream defaults. supported values and request encoding depend on the model and serving endpoint; a common setting must not imply an identical effort scale across models. unsupported explicit settings fail clearly rather than being silently ignored or translated to a nearby level. the work order's [subscription reasoning-effort capability table](openai-subscription-models-work-order.md#subscription-reasoning-effort-capabilities) defines exact sol/astra values, omission behavior, wire encoding, and source-versus-live evidence. those built-in capabilities will drive Go registry validation and request encoding; users only select an effort in their registry entry. qwen's endpoint-specific mapping follows below.

the official [qwen3.8-27b model card](https://huggingface.co/Qwen/Qwen3.8-27B#api-usage) documents thinking enabled by default, effort levels 'low', 'medium', and 'xhigh' (the default), and examples using `chat_template_kwargs.enable_thinking` plus `reasoning_effort`. the infrastructure report below establishes different serving engines behind pane's two aliases. neither the upstream model id nor a host-flavored alias is a sufficient request-capability declaration. do not assume other qwen generations share these controls.

the selected reasoning settings remain fixed for every tool round in the turn, alongside the model connection. effort is distinct from an output-token cap, collapsing the thinking display, and replaying prior reasoning. qwen's model-card guidance on preserved thinking informs the narrow active-tool-loop exception below, not blanket replay of saved display thinking.

### deployed qwen findings

the infrastructure owner's report, 'qwen3.8-27b reasoning controls: the deployed request contract', dated 2026-09-22 and supplied at `/home/michael/Sandbox/qwen-reasoning-controls.md`, distinguishes source inspection, production-log observations, metadata probes, and unverified behavior. the findings below are attributed to that report, not independently tested by pane's design agent.

the fortyfive owner's follow-up is available at `/home/michael/Sandbox/qwen38/qwen3.8-request-contract.md`, with the embedded template at `qwen3.8-chat-template.jinja` and deployment history at `qwen3.8-fortyfive-sizing.md` in the same directory. it corrects the engine identity to llama.cpp commit '0adcc3b', checkout tag 'b10502': 'build 1' is a local-build artifact, not an upstream release. source lives at `~/local/llama.cpp-src` on fortyfive; `~/local/llama-b10502-cuda` points to build output. the follow-up is source/config inspection plus existing observations, not new inference tests. the copied template was also read directly during this design pass.

the subsequent verification supplement (section 9 only) is at `/home/michael/Sandbox/qwen3.8-request-contract.md`; it does not replace the earlier full source contract. the owner ran 16 authorized synthetic `/apply-template` checks. raw artifacts live in `/home/michael/Sandbox/qwen38/contract/out/`, with selected reasoning-history, recovery, and error artifacts inspected directly here. effort rendering, disable equivalence, precedence, conflicts, preservation, and recovery instruction placement are now prompt-verified on fortyfive. no generation ran; sampling, response parsing, and successful tool execution remain unverified by these checks.

| connection | pre-spike established contract | unverified at that point |
|---|---|---|
| 'qwen3.8-27b@eleven' | ninfer revision 'a140e7ae'; source accepts top-level `reasoning_effort` values 'none', 'low', 'medium', 'xhigh'; production logs show default 'xhigh', thinking enabled, and preserved thinking disabled | live non-default effort and disabled-thinking paths |
| 'qwen3.8-27b@fortyfive' | llama.cpp commit '0adcc3b', tag 'b10502'; prompt-verified top-level effort, 'none' disable rendering, 'low'/'medium'/'xhigh' instructions, default 'xhigh', invalid 'high' HTTP 500, and preservation boundary | effective sampling, generated response parsing/streaming, and completed tool-round continuation |

the table above records the pre-spike evidence. the subsequent [live results](openai-subscription-models-spike-results.md) establish 'low' and 'none' generation, one approved tool loop with active-round reasoning replay, and forced-final recovery on both engines. effective sampling remains unknown. reported high cache reuse supports continuity but is not alone proof of byte-for-byte prompt identity.

the aliases need no rename: they identify connections, not inference engines. both currently advertise upstream model 'qwen3.8-27b', but their request and error contracts cannot be assumed equivalent. the accepted `compatibility_profile` declaration selects those conventions explicitly rather than inferring them from an alias or model name.

ninfer rejects unsupported levels and contradictory controls rather than silently coercing them. disabling thinking selects ninfer's non-thinking sampling defaults automatically. fortyfive's wrapper instead pins thinking-mode sampling settings; its report identifies explicit `temperature: 0.7`, `top_p: 0.8`, and `top_k: 20` overrides for non-thinking requests, with request override handling established from source. the work order preserves these distinct presets rather than adding ninfer's presence penalty to fortyfive by assumption. effective sampling remains a verification limit, not permission to change the preset or add an unrestricted request-body override.

both deployed qwen connections therefore have a source-established top-level `reasoning_effort` mapping for 'none', 'low', 'medium', and 'xhigh'. pane should emit only that control, not a potentially conflicting `enable_thinking` alongside it. fortyfive's parser and template can disagree under contradictory inputs; unsupported enabled-thinking efforts raise template exceptions reported as HTTP 500, while unknown kwargs may be silently ignored. validate capabilities before sending and do not treat every upstream 500 as transient. 'medium' injects no extra effort instruction in this template; levels describe model guidance, not measured token quotas.

fortyfive's prompt checks confirm that omitted effort equals explicit 'xhigh', and 'none' equals `enable_thinking: false` at the rendered-prompt level. the contradictory 'none' plus `enable_thinking: true` request instead equals default 'xhigh'. prevent that combination in pane rather than relying on endpoint precedence. the observed unsupported-'high' error is suitable for a diagnostic fixture, but message matching is not a substitute for pane-side capability validation and must not reclassify every HTTP 500 as a configuration error.

retain an explicit qwen `max_tokens` budget: ninfer charges thinking and answer tokens to the same cap, and the report records prior truncation when a client inherited the server's smaller default. effort is not an independent thinking-token budget. the reported production requests exercise streaming with tools supplied at default effort; they do not establish successful tool execution at every proposed effort level.

fortyfive additionally accepts a per-request `reasoning_budget_tokens` cap according to source. it forces a thinking-end marker when exhausted and is separate from the shared output cap. live behavior is unverified. hard reasoning budgets are deferred from the first effort-control schema pending an explicit need and tests of answer/tool completion after forced closure.

effort affects rendered-prefix identity and therefore cache reuse. retain between-turn preset switching, with its possible re-prefill cost documented; do not introduce conversation-long pinning merely to preserve cache hits. the size of the switching penalty needs measurement. completed-turn thinking remains display-only; the accepted active-tool-loop exception follows below. interrupted requests follow the explicit new-turn recovery policy rather than resuming lost qwen reasoning.

fortyfive's prompt-level investigation and the subsequent bounded generation/tool checks on both engines are complete as recorded in the spike results. effective sampling and any unexercised levels remain distinct from those passes; new probes require operator approval. hard reasoning-budget experiments remain deferred. validate resolved request settings and completed answer/tool behavior, not a promise that one lower-effort sample must produce fewer tokens. sampling values require request-scoped effective-setting evidence, not inference from one answer or static server defaults. cache-cost claims require a controlled multi-request comparison rather than a single probe.

### qwen tool-loop replay exception

the fortyfive template preserves reasoning by default. setting canonical `chat_template_kwargs.preserve_reasoning: false` maps to its `preserve_thinking` variable and omits reasoning blocks for completed turns, but assistant messages after the latest real user query retain their blocks regardless. template inspection and live prompt checks confirm that missing `reasoning_content` renders an empty block, and that both preservation spellings render identically. this establishes a loss of faithful continuation, not a demonstrated API requirement or measured quality regression; the report's claim that replay is required must be read with that distinction.

accepted decision: retain provider-originated reasoning inside the active go tool loop and replay it only to the compatible qwen connection for that turn; drop closed-turn reasoning from the outgoing prompt. this is a narrow exception to the display-only rule, not blanket replay of browser thinking, historical turns, or another model's reasoning. map preservation controls separately for each engine: ninfer does not accept llama.cpp's canonical kwarg spelling. cancellation, retry, reload, and any continuation outside the active request still need the explicit lifecycle contract below; this request-local decision does not solve them.

that replay state never enters `round_ready`, `round_complete`, durable continuation envelopes, or stored turn recovery records. visible thinking may still be displayed and saved through its existing separate display path; it is never used to reconstruct provider replay data after the active Go request ends. qwen's request-local continuation and subscription models' durable continuation are deliberately different lifetimes.

pane's repeated-tool-failure path appends a system instruction after history; fortyfive's prompt checks verify rejection with HTTP 500. merging the recovery instruction into the initial system message and omitting tools renders successfully while retaining prior tool-call/result history; the subsequent spike also verified a generated forced-final answer on both qwen engines. accepted adapter behavior: make that adjustment only in the outgoing recovery request, never rewrite the stored conversation. do not substitute a trailing user message within the active loop merely to avoid changing the system prefix: from the inspected template, a new real user message moves `last_query_index`, potentially reclassifying active tool-round reasoning as completed history. that alternative changes semantics as well as caching. this in-request forced-final path is distinct from explicit new-turn recovery after interruption.

## authentication ownership

the first version uses cli-managed login. the command surface is `pane auth login openai`, `pane auth status openai`, and `pane auth logout openai`, with browser and device-code login options. device-code login supports installations where the browser and pane service run on different machines, subject to the account supporting that flow.

pane obtains its credentials through its own sign-in and manages them independently of pi and the codex cli. its private `auth.json` lives beside the global configuration, respecting `XDG_CONFIG_HOME`, with owner-only permissions. access and refresh tokens stay on the go side, outside configuration yaml, browser storage, conversation records, exports, and ordinary logs. run the authentication commands as the operating-system user running the service.

one credential manager supplies current credentials to all openai model connections. login, refresh, and logout coordinate through a shared lock and atomic credential writes, including across cli and service processes. the service picks up completed login without restarting. local logout removes pane's stored credentials; it does not claim to revoke every session for the openai account.

temporary network failures preserve credentials. invalid or revoked credentials call for another login. exhausted subscription allowance is reported as such; pane does not silently switch to separately billed api access.

## conversation continuity and switching

switching models is allowed between turns. one selected model owns the complete response to a submitted request, including all tool rounds and pending approvals. the selector cannot change the model while that response is active. cancellation and partially completed rounds follow the interruption/recovery contract below and its concrete work-order projection.

user messages, assistant answers, tool calls, and tool results remain available when switching between qwen, sol, and astra. switching adapts the outgoing representation without rewriting or discarding the stored conversation. the promise is continuity of the recorded conversation; opaque reasoning continuity is conditional on compatibility.

assistant messages record the model connection that produced them. a usage record naming the most recently selected alias is insufficient provenance for older messages, and an alias alone may later resolve to a different connection. replay identity follows the accepted rules below and the work order's versioned serialized representation.

provider continuation data has a separate role from displayed thinking. opaque reasoning items and other replay metadata survive completed rounds, storage, reload, and later full-history requests. the destination provider includes only compatible data when constructing its request. completed-turn thinking remains display-only; the accepted qwen active-tool-loop exception is request-local. displayed thinking is never silently converted into assistant text for another model.

provider adapters own request conversion and upstream event interpretation. pane owns the mcp tool loop and its approval decisions. conversions must preserve the association between a tool call and its result, even where an upstream protocol uses additional item ids or different id constraints. the browser continues to consume pane's event protocol rather than implementing openai's transport.

### adapter and round contract

the accepted boundary is a provider-neutral round, not a general plugin framework. one adapter call performs one upstream generation round. it accepts portable history, tools, and round intent such as normal generation or forced-final recovery; its connection and settings are already resolved and fixed for the active turn. the adapter applies its compatibility profile internally. the shared loop alone executes tools through the mcp manager, manages approval, and decides whether another round is needed. the adapter never executes tools or owns a second approval path.

the accepted stream contract separates progressive display events from a finalized assistant round. adapters normalize text, thinking, and tool-argument updates without manufacturing chat-completions-shaped chunks for a different protocol. the final round carries the authoritative text/tool calls, usage, completion status, origin, and required continuation data. tool-call previews are not executable; execution waits for validated complete calls and a successful tool-requesting round. chat completions requires `finish_reason: 'tool_calls'` followed by `[DONE]`; subscription Responses requires a successful completed terminal response and finalized valid function-call items. normal successful answers without calls finish without tool execution. truncated, incomplete, failed, cancelled, filtered, missing, conflicting, or unknown terminal states execute nothing from that round, even if some calls look complete. the work order defines the exact normalization matrix, subscription compatibility spelling, and fixture coverage; stream termination or item completion alone never authorizes tools.

portable messages remain the cross-provider record. durable provider continuation belongs in a versioned per-assistant-round envelope with origin and compatibility information, not in display text and not solely in a provider-side conversation id. the browser preserves that envelope through finalized-round events, save/reload, and subsequent full-history requests, but does not interpret it. the destination adapter validates browser-supplied envelopes and selects compatible replay data; credentials are never part of the envelope. the work order specifies identity, stale/invalid-data handling, byte limits, and interrupted-round persistence. active qwen tool-loop reasoning is request-local under the accepted exception, distinct from durable subscription continuation.

### replay identity

accepted replay identity follows the resolved connection, not its display alias. conservative compatibility checks cover provider/protocol, replay-format version, upstream model, endpoint or provider service identity, compatibility profile, and account scope where applicable. use a non-secret account binding, never an access/refresh token or api-key fingerprint. token refresh must not invalidate continuation merely because credential bytes changed. alias and effective reasoning effort remain useful provenance; an alias rename alone does not invalidate replay, and effort compatibility is a provider rule to verify rather than an assumption based on equal model ids.

the work order's [subscription replay identity format](openai-subscription-models-work-order.md#subscription-replay-identity-format) pins the full service string, account-hash domain and byte encoding, digest representation, and decoded-field comparison under the continuation format version. synthetic golden fixtures guard against accidental identity drift. changing those rules requires an explicit versioned compatibility decision, not silently rewriting saved continuation.

matching identity makes saved data eligible for replay, not automatically valid: the adapter also checks item ordering, call/result associations, and attachment to the recorded assistant round. if an alias points to a different connection or a different account is selected, preserve its old envelopes on disk but exclude incompatible data from the outgoing request. portable answers and tool history still accompany the selected connection; old conversations without provenance remain usable as portable history. replay after switching back is conditional on these same checks, not guaranteed merely by selecting the old alias.

this is an accepted fail-closed policy for provider-specific continuation, not permission to lose the conversation or silently retry a failed tool turn. verify that each adapter can reconstruct an acceptable portable request without incompatible continuation; where it cannot, report the limitation before sending. malformed envelopes and interrupted-round recovery need their own validation/lifecycle rules rather than being classified as routine model switching.

### interruption and recovery policy

current behavior has a replay hazard: `retryLastRequest` in `ui/src/hooks/useChat.ts` resubmits the original request snapshot, not the history incorporating subsequently committed tool rounds. individual `tool_call_result` events update transient tool state; messages are committed on `round_complete`, which the go loop emits after the entire batch. cancellation clears transient state. the integration must not inherit this as safe retry semantics.

accepted invariant: interruption does not roll back external tool effects. stop prevents further dispatch and cancels ongoing work best-effort; an interrupted dispatched call without a confirmed result has unknown outcome, not confirmed failure or non-execution. a lost browser stream may also hide a dispatch notification, so absence of that notification cannot establish that no tool ran. incomplete model calls never become executable merely because the stream ended.

preserve observed tool-call identities and outcomes incrementally in the conversation's recovery record, not only after a whole batch. distinguish completed/failed calls, calls known not to have been dispatched, and unresolved outcomes. browser receipt and asynchronous mirroring are not crash-safe server acknowledgements; do not claim exactly-once execution or guaranteed recovery after disconnect/reload without a separate durable execution mechanism. never manufacture a successful tool result to satisfy a provider's message pairing rules.

the tool executor explicitly reports 'not_dispatched', 'result_received', or 'unknown', independently of its error diagnostics. the MCP boundary assigns that outcome from dispatch/reply evidence, not error-message heuristics; a returned tool failure is still a received result, while transport failure after possible dispatch stays unknown. the shared loop maps that distinction into recovery state and stops on unknown rather than asking the model to repeat the call. approvals and validation remain pane-owned; adapters never execute tools.

accepted first version: no transparent resumption of interrupted tool execution. retry from scratch only when non-execution is established; after recorded tool work, offer explicit continuation as a new user turn from preserved results, not replay of the original request. unresolved outcomes require user reconciliation before tool-enabled continuation. the work order's [portable recovery projection](openai-subscription-models-work-order.md#portable-recovery-projection) preserves finalized assistant calls, follows them with one tool-role message per call in call order, then places the operator reconciliation note before the new user message. received results remain exact; known non-execution and acknowledged unknown outcomes use explicitly labeled pane recovery placeholders, never invented MCP results. original execution states remain unchanged. a new model request can itself request repeated work, so recorded history alone is not an exactly-once guarantee. deliberate repeat remains a distinct user action with the side-effect risk made explicit.

the browser includes prior turn recovery records alongside the full history in `/api/chat`, excluding the fresh turn's saved in-progress marker. reuse the stored record shape, bind it to user/round/call identities and the submitted messages, and preserve terminal evidence and explicit operator reconciliation. the stateless backend validates those bindings and rejects unresolved interruptions before contacting a model; it never reads the conversation store. an empty call list does not resolve an unfinished turn. explicit reconciliation permits a new turn with truthful results or operator-attributed notes; acknowledged unknown outcomes stay unknown and never qualify as proof for safe retry. these are consistency checks on client-supplied history, not authentication of external tool effects. legacy clients without the new contract retain legacy access but gain no recovery-validation guarantee.

the submitted fresh turn id binds to exactly one message: the final user message. it matches the saved pre-send marker but has no entry in the outgoing prior recovery records. every other turn-tagged message must bind to a supplied prior record; the fresh final user message is the explicit exception. duplicate fresh ids, fresh ids on other roles or positions, and prior records reusing that id are rejected before generation. saving and POST use the same allocated id so the returned lifecycle has one unambiguous owner.

this also respects request-local qwen reasoning: do not promise exact continuation of its interrupted active tool loop after that memory is lost. a new user turn can use completed portable results without requiring historical reasoning replay. the work order separates partial display text from finalized replay data and specifies retry/continue/reconcile actions; presentation details remain implementation work within those rules. a durable execution ledger or resumable job service would be an explicit expansion, not an incidental addition to the provider adapter.

the browser save boundary is accepted: persist the user message and an in-progress turn marker through the existing store facade and await that save before issuing the chat POST. save failure prevents sending. an unfinished marker after reload triggers conservative reconciliation even if no tool event was received. subsequent outcome saves remain incremental and asynchronous; this adds neither a server execution ledger nor exactly-once guarantees. a lost terminal save can therefore require reconciliation after actual success. the current multi-browser last-writer limitation remains.

prepare that save as a private candidate outside the normal conversation mirror; only install it after acknowledgement. failure preserves the composer and discards the local candidate so later unrelated saves cannot resurrect it. briefly block same-tab conversation switching, creation, deletion, reset, duplicate sends, and edits to the candidate's conversation through the save-and-send decision. successful acknowledgement installs the candidate and starts the original owner's POST before releasing the guard, without an intervening asynchronous step. this prevents normal navigation from leaving a saved marker for an unsent request. browser teardown and ambiguous write failures remain conservative exceptions: a marker may exist on disk even when no POST occurred, and stale callbacks must not initiate one.

## availability policy

accepted policy: keep all configured aliases in the selector even when a provider is signed out or temporarily failing. selecting a signed-out subscription model shows `pane auth login openai` and disables sending for that selection, without blocking startup or access to other connections. preserve the selection and draft; never silently select another model or billing route. credentials present means configured to attempt a request, not verified account entitlement or upstream health.

separate local configuration validity, authentication state, and the last observed runtime failure. invalid provider/profile combinations or unsupported explicit effort values are startup configuration errors. missing/revoked credentials call for login; an expired access token with refresh credentials calls for refresh, not immediate relogin. network failures, allowance exhaustion, and model-access errors are reported where they occur, with their scope kept to the affected connection/account/model as supported by evidence. do not create a permanent disabled state from one transient failure, invent reset times, or imply that a configured model is guaranteed available.

registry listing remains local and does not probe each upstream. expose safe status metadata separately from credential contents and update it after cli login/logout and relevant request outcomes, without requiring a pane restart. refresh/retry affordances must respect the accepted tool-recovery policy; status refresh is not permission to rerun a failed turn. the work order specifies the status payload and bounded refresh cadence; browser presentation implements those rules.

## scenarios the design must support

- sign in once through the cli, then select either configured openai model in the running browser interface.
- begin with qwen, switch to sol after a completed response, and continue with the preceding answers and tool results intact.
- let sol execute an approved mcp tool, finish the response, reload pane, and continue on sol with compatible continuation data preserved.
- switch from sol to astra and later back to sol. preserve the original record throughout; determine replay compatibility per message rather than treating the whole conversation as belonging to its latest model.
- encounter expired credentials or subscription exhaustion without losing committed conversation history or changing the billing method.
- select qwen aliases with different thinking presets and verify both the outgoing controls and the serving endpoint's behavior; retain the same preset throughout a tool loop.

## seam census

| boundary | accepted call and rationale | revisit condition |
|---|---|---|
| pane / external harness | native model provider; pane's control of conversation tooling is central | native backend compatibility proves untenable |
| registry / credentials | separate; model aliases select connections while one provider account owns rotating credentials | a concrete need for multiple accounts |
| provider / compatibility profile | transport/authentication separate from built-in model/engine conventions; no arbitrary request overrides | a new connection cannot be represented by a bounded profile |
| conversation / transport | separate; preserve a portable conversation and origin-tagged opaque continuation data, interpreted by the destination adapter | a required upstream capability cannot be represented faithfully |
| thinking display / replay | separate, with request-local qwen tool-loop replay; completed-turn thinking is not automatically input | a concrete need for broader readable reasoning replay |
| selection / active turn | fix the model for the whole turn, including approvals and tool rounds | an explicit design for mid-turn handoff |
| interruption / tool effects | no rollback claim or transparent execution resumption; reconcile unknown outcomes | an explicit durable-execution design |
| configuration / availability | invalid config fails startup; missing login and runtime failures leave other connections usable | new evidence requiring broader failure isolation |

configuration errors belong at startup; runtime authentication and upstream failures follow the accepted availability policy without blocking unrelated connections.

## configuration validation and credential isolation

resolve the provider before endpoint and key inheritance. for `openai-chat-completions`, retain existing endpoint/key inheritance and explicit-empty-key behavior. for `openai-codex`, do not inherit the top-level endpoint or api key: use only provider-owned destinations and the credential manager established by pane's login. accepted first-version rule: reject explicit per-model `endpoint` or `api_key` fields on a subscription entry rather than ignoring them. presence must be distinguishable from omission for validation, including values retained by the yaml cascade.

the subscription transport must constrain authenticated requests to its approved provider destinations and refuse redirects that would carry credentials elsewhere. do not allow request history, browser metadata, or compatibility profiles to select a credential destination. endpoint overrides and subscription reverse-proxy configuration are not first-version capabilities.

validate provider/profile combinations and explicit reasoning settings before startup, using the supported capability contract rather than probing account entitlement. preserve existing behavior for configurations without new controls, except the explicitly required execution-safety fixes. explicit output caps must not be silently ignored: where the subscription route lacks a verified mapping for `max_tokens`, reject that setting with an actionable error until a supported mapping is established. do not copy local-model context/output defaults into subscription entries; without an operator-configured context window, use the existing unknown `?` display.

## remaining verification

the capability, replay, recovery, availability, and credential-isolation contracts are now specified in the work order. implementation must verify them against production code with its local/synthetic test matrix; the prototype is evidence, not a substitute for those tests.

the live gaps are same-round encrypted reasoning/function-call replay, real token refresh, browser/PKCE login, and effective qwen sampling. subscription context capacity remains unknown and may stay so with the accepted unknown meter. before production acceptance, michael must explicitly disposition unverified live cases; new account traffic requires separate bounded authorization. source-supported effort values beyond the live-tested 'low'/'medium' subscription cases are not relabeled live passes.

## verification spike and follow-up

the bounded phase and its authorized follow-up are complete and closed. the [spike brief](openai-subscription-models-spike-brief.md) retains the original plan; the [results report](openai-subscription-models-spike-results.md) contains source pins, the verified/failed/unknown matrix, and sanitized evidence. those findings informed the reviewed work order. no production implementation has started.

the spike's authorization does not carry forward. new sign-in, refresh, or generation needs separate operator approval with endpoints and request/time ceilings; effort/timeouts are not enforceable token caps. use synthetic conversations and harmless tools for local tests, never another harness's credentials or private captures. preserve the distinction between mocked handling and live provider evidence, and do not expand scope to hard reasoning-budget experiments or context benchmarking.

### live-run evidence and remaining verification

the [spike results](openai-subscription-models-spike-results.md) report 21 of 23 approved requests with no retries. device login into the private scratch store worked, originator 'pane' was accepted, both subscription models streamed at effort 'low', a sol tool loop and fresh-process replay of tool-call items succeeded, and the synthetic qwen → sol → astra → sol sequence was accepted. empty instructions and a tools-omitted forced-final request were also accepted. pi '0.87.1' is pinned by file hashes in that report, superseding the earlier '0.85.1' inspection. the report's test count is the spike agent's result, not a fresh design-agent test run.

the route specifically rejected `max_output_tokens` with HTTP 400. this supports the accepted configuration rule to reject explicit subscription `max_tokens` while no supported mapping is verified; it does not establish that every conceivable output-control field has been tested. effort and local timeouts are not guaranteed upstream token or cost ceilings. the reported access-token lifetime describes that login, not a promised provider lifetime.

neither model emitted encrypted reasoning on the initial short effort-'low' prompts. the separately authorized follow-up used six requests total, three per model at effort 'medium', with no retries, refresh, or failures. the updated report records nonempty encrypted reasoning in each post-tool answer round, byte equality from streamed item through saved document and fresh-process replay, compatible-envelope selection, stored item order preserved on the wire, and completed backend responses. this verifies reasoning-plus-message continuation without portable fallback for both sol and astra.

the remaining live replay gap is reasoning immediately followed by a function call in the same round: both tool-requesting rounds contained only the call. the prototype's `TestToolCallRoundAndEnvelopeReplay` explicitly checks the synthetic reasoning/function-call/output order, encrypted payload, and provider ids; that test source was inspected here, not rerun. the work order carries the missing live case as a specific acceptance requirement, not a claim of universal replay coverage. it does not block starting the reviewed implementation; any further live exercise needs its own bounded authorization.

astra's verified encrypted reasoning had an empty display summary. continuation capture, persistence, and replay must not depend on seeing `thinking_delta` events or nonempty thinking text; lack of display text does not establish disabled reasoning. include an empty-summary regression fixture in production adapter/frontend tests.

the operator-directed spike cleanup is complete: the agent removed the private login, copied qwen key, and account-bound captures/documents, preserving sanitized evidence and non-sensitive run records. the live spike is closed; new login, refresh, or inference needs separate authorization. no private credential/capture contents were inspected during this design pass. qwen sampling, subscription context capacity, real refresh, browser login, and production browser-mirror recovery still require evidence or explicit limitations; prototype schemas and its synced-file recorder are not accepted as a production implementation merely because the live requests succeeded.

the [reviewed work order](openai-subscription-models-work-order.md) maps the accepted design and browser save barrier to production stages, record/event schemas, limits, and tests. mercurius round 3 returned 'ready_to_build'; all advisory clarifications are applied and the review is closed. implementation proceeds only through the operator's handoff, with each stage reviewed by terminus.

## deferred (and why)

- web-managed sign-in: cli setup is accepted for the first version and fits the persistent service deployment.
- multiple openai accounts, account pooling, and automatic provider failover: the initial use is one human's subscription; these add credential and billing decisions without a current need.
- a separate paid openai responses provider: it may share protocol code later, but subscription access is the subject of this design.
- mid-turn model changes: the accepted model ownership rule gives tool execution and approvals one stable context.
- replaying readable thinking from completed turns, per-model historical usage maps, compaction, and additional modalities: these remain separate work. opaque continuation support and the accepted qwen active-tool-loop exception do not settle the broader historical-reasoning roadmap card.
- authenticating users to pane itself: provider login does not implement the separate web-access authentication roadmap item.

## evidence and limits

pi's [oauth implementation](https://github.com/earendil-works/pi/blob/main/packages/ai/src/auth/oauth/openai-codex.ts), [refresh coordination](https://github.com/earendil-works/pi/blob/main/packages/ai/src/auth/resolve.ts), [codex transport](https://github.com/earendil-works/pi/blob/main/packages/ai/src/api/openai-codex-responses.ts), and [message conversion](https://github.com/earendil-works/pi/blob/main/packages/ai/src/api/openai-responses-shared.ts) provide implementation evidence. initial exploration inspected '0.85.1'; the spike report supersedes that baseline with '0.87.1' and exact file hashes. these upstream-main links are navigation aids, not the version pin.

openai documents [subscription authentication](https://learn.chatgpt.com/docs/auth) and explicitly names pi among preferred-tool choices on its [codex for open source page](https://developers.openai.com/community/codex-for-oss). michael has used pi with his codex subscription for most of a year without issues. the design proceeds on that practical evidence. this does not establish a stable public contract for pane's exact backend integration or blanket contractual clearance; written clarification is not a prerequisite imposed on this arc.
