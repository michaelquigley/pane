# openai subscription spike harness

Isolated prototype for `docs/future/openai-subscription-models-spike-brief.md`. It is its own Go module (`spike/openai-subscription/go.mod`), so pane's `go test ./...`, `go vet ./...`, and `make test` never see it. Nothing here is production code. Results and the run plan live in `docs/future/openai-subscription-models-spike-results.md`.

## layout

| package | role |
|---|---|
| `round` | proposed provider-neutral contract: portable message, origin/identity, continuation envelope, normalized events, finalized round, classified errors |
| `codex` | subscription adapter: responses SSE, `store=false`, full history, envelope replay/filtering, destination-locked credential transport |
| `qwen` | chat-completions adapter with `qwen3.8-ninfer` / `qwen3.8-llamacpp` profiles: effort, sampling, preserve kwarg, request-local active-round reasoning, forced-final placement |
| `auth` | private scratch credential store (0700/0600, flock, atomic write), double-checked refresh, browser PKCE and device-code login |
| `loop` | the harness's tool loop (sole approver/executor) and the interrupted-turn recovery record |
| `convo` | session-document stand-in (portable messages + turn records) |
| `budget` | per-process and persisted (cross-process) request ceilings |
| `mcpfix/mcpadd` | stdio mcp fixture: one `add` tool; counts executions to `PANE_SPIKE_EXEC_LOG` |
| `e2e` | real adapters through the locked transport, real fixture, fresh-process reload, switching sequence |
| `cmd/spike` | operator CLI for the approved live run |

## offline verification

```bash
cd spike/openai-subscription
go vet ./...
go test -count=1 -race ./...
```

These tests use local mock upstreams and synthetic fixtures (`testdata/codex/*.sse`). Passing them establishes local handling only, not provider behavior. Test builds go to temp directories and are removed; nothing is installed.

## live run (only after approval)

Everything the CLI writes lives under `--state` (default `~/.local/state/pane-spike`, 0700). No network command runs without a ledger created from the approved ceilings; every generation POST, including failures, spends one unit before it is sent. Nothing retries automatically.

```bash
go build -o "$STATE/bin/spike" ./cmd/spike
go build -o "$STATE/bin/mcpadd" ./mcpfix/mcpadd
spike budget-init --total N --per sol=..,astra=..,qwen-eleven=..,qwen-fortyfive=..,auth=.. --wall 60m
spike login --method device      # prints a verification url and user code; tokens never printed
spike status                     # signed_in, expiry, derived account scope only
spike turn --conn sol --effort low --doc "$STATE/docs/s1.json" --prompt "reply with exactly: ready"
spike turn --conn sol --effort low --tools --fixture "$STATE/bin/mcpadd" --doc "$STATE/docs/tool.json" --prompt "..."
spike turn --conn sol --effort low --doc "$STATE/docs/tool.json" --prompt "..."   # a fresh process: reload + continue
spike seed --doc "$STATE/docs/ff.json" && spike round --conn qwen-fortyfive --forced-final --doc "$STATE/docs/ff.json"
spike budget
```

A separately authorized budget gets its own ledger (`--ledger budget-followup.json` on `budget-init`, `turn`, `round`, and `budget`); earlier ledgers stay untouched. `--approve-once` approves only the first tool call of a turn. `--capture-requests` saves each outgoing generation body privately beside the capture. `scripts/replaycheck.py a <doc> <raw...>` checks that stored encrypted reasoning equals the streamed items, and `scripts/replaycheck.py b <doc> <summary> <request>` checks replay decisions and in-order, byte-equal transmission. Both print counts, booleans, and item types only.

Each invocation prints the path of a private summary (finish, usage, value-free event shapes). Raw SSE payloads go to `captures/*.raw.jsonl` (0600); they can contain account-bound encrypted reasoning and must not enter the repository. The qwen bearer key is read from an owner-only file (`<state>/qwen.key` by default), never from arguments.
