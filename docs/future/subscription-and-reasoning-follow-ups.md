# subscription and reasoning follow-ups

unscoped options retained after the subscription-model arc, 2026-09-28. built behavior and accepted verification limits live in [the current design record](../current/pane.md#subscription-continuation-and-acceptance-limits). these are revisit conditions, not another implementation stage or authorization for live traffic.

- **auth conveniences:** web-managed sign-in, browser/PKCE login, or automatic refresh only if occasional device-code CLI login becomes a real burden. browser login and refresh were deliberately removed; reintroduction needs an operator decision and verification.
- **accounts and providers:** multiple accounts, pooling, automatic failover, and a separately billed Responses provider need an actual use case and explicit credential/billing policy. the current provider serves one subscription account and never changes billing routes silently.
- **mid-turn model handoff:** requires a separate design for ownership of approvals, execution, and continuation. between-turn switching is already built.
- **hard reasoning budgets:** investigate `reasoning_budget_tokens` on the deployed llama.cpp engine only if effort presets are insufficient; establish answer/tool completion after forced reasoning closure before exposing it. no hard-budget or context benchmark is authorized by the completed arc.
- **readable historical reasoning:** the [reasoning-echo card](roadmap/round-trip-reasoning-echo-in-re-sent-history.md) remains broader than request-local qwen reasoning or durable encrypted subscription continuation. revisit only for a demonstrated coherence need, not by copying saved display text into requests.
- **usage, compaction, and modalities:** [per-model usage history](roadmap/multi-model-conversation-records.md) and [automatic compaction](roadmap/auto-compaction.md) remain separate work; image/multimodal input is also unbuilt. continuation support alone does not define safe compaction of provider items and tool pairs.
- **stronger recovery or integrity:** a durable execution ledger/resumable service, authenticated envelopes, and [cross-client session versioning](roadmap/server-assigned-session-versioning.md) would change the current supervised, stateless/full-history design. do not add them merely to close its documented residuals.
- **pane access control:** provider login is not authentication to pane's web interface; that is the separate [authentication card](roadmap/authentication.md).

astra same-round pairing and qwen effective sampling are accepted operational verification limits, not blockers assigned to this future work. exercise astra through ordinary use and inspect qwen's request-correlated inference logs; keep any later claims bounded by what was actually observed.
