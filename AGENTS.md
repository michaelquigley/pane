# pane

a thin pane of glass between a human and an LLM. Go binary with an embedded React web frontend. first-class MCP stdio support.

## project context

see `README.md` for the user-facing overview. design docs land in `docs/` — `docs/current/` for built behavior, `docs/future/` for forward-looking specs. `docs/current/pane.md` holds the original design document.

pane is a single-binary chat client that connects to OpenAI-compatible completions endpoints and spawns local MCP servers as child processes for tool use. an optional explicit model registry binds UI-visible aliases to fixed endpoints, upstream model ids, credentials, context windows, and output caps; configurations without it retain the original single-endpoint behavior. conversations live on disk as one opaque JSON file each under a configurable data directory; the browser holds a working copy and per-browser view state. the chat path stays stateless -- every `/api/chat` request carries the full history.

## tech stack

- **language**: Go 1.26
- **CLI**: github.com/spf13/cobra
- **config**: github.com/michaelquigley/df/dd (YAML binding)
- **logging**: github.com/michaelquigley/df/dl (structured slog wrapper)
- **MCP**: github.com/mark3labs/mcp-go (stdio client)
- **LLM client**: hand-rolled OpenAI-compatible HTTP client (no third-party dependency)
- **frontend**: React 19 + TypeScript + Vite, embedded via go:embed; Vitest for focused behavior tests
- **markdown**: react-markdown + remark-gfm + react-syntax-highlighter
- **fonts**: Source Serif 4 + JetBrains Mono, bundled via @fontsource-variable packages (embedded in the binary, no runtime font fetch)
- **versioning**: github.com/michaelquigley/push/build — version/commit/date stamped via ldflags in CI (`.github/workflows/ci.yml`); developer builds report `v0.1.x [developer build]`

## package structure

```
pane/
├── cmd/pane/                   # cobra CLI entrypoint
│   ├── main.go                 # root command (runs server), --verbose, --config
│   ├── version.go              # version subcommand
│   └── new.go                  # generate pane.yaml
├── internal/
│   ├── config/                 # Config structs, YAML cascade loader, validation
│   ├── llm/                    # OpenAI-compatible HTTP client and streaming
│   │   ├── client.go           # NewClient, ListModels, StreamChat
│   │   ├── types.go            # ChatRequest, Message, Tool, ToolCall, StreamChunk
│   │   ├── stream.go           # SSE stream reader (parses OpenAI format)
│   │   └── toolloop.go         # tool-call loop, ToolExecutor/ApprovalRegistry interfaces
│   ├── mcp/                    # MCP server lifecycle manager
│   │   ├── manager.go          # spawn, init, discover, execute, stop
│   │   └── tool.go             # ToolInfo, MCP-to-OpenAI translation, namespace
│   ├── session/                # file-backed conversation store
│   │   └── store.go            # Store, List/Get/Save/Delete, atomic writes, id safety
│   ├── sse/                    # SSE event writer for pane's streaming protocol
│   │   └── writer.go           # Writer, Send, event data types
│   └── api/                    # HTTP API handlers
│       ├── api.go              # API struct, route registration
│       ├── chat.go             # POST /api/chat (tool loop integration)
│       ├── modelClients.go     # construct one immutable LLM client per configured alias
│       ├── models.go           # GET /api/models
│       ├── tools.go            # GET /api/tools
│       ├── sessions.go         # /api/sessions CRUD handlers
│       └── approve.go          # POST /api/tools/approve, ApprovalRegistry
├── ui/                         # frontend source + embed (zrok pattern)
│   ├── embed.go                # //go:embed dist (build tag: !no_ui)
│   ├── embed_stub.go           # empty FS (build tag: no_ui)
│   ├── middleware.go           # SPA middleware: /api/ passthrough, index.html fallback
│   └── src/
│       ├── App.tsx             # top-level layout, the conversation surface
│       ├── types.ts            # Conversation, Message, ToolCall, SSEEvent, etc.
│       ├── lib/
│       │   ├── sse.ts          # SSE stream parser (pane's protocol)
│       │   ├── usageRecord.ts  # attach selected model aliases to usage events
│       │   ├── sessionStore.ts   # SessionStore interface + backend adapter
│       │   └── exportMarkdown.ts # conversation-to-markdown export
│       ├── hooks/
│       │   ├── useChat.ts      # SSE streaming, tool call state machine, approvals
│       │   ├── useConfig.ts    # GET /api/config
│       │   ├── useSessions.ts  # the mirror hook: hydration, diff-mirror, serial chain
│       │   ├── useLocalStorage.ts
│       │   ├── useModels.ts    # GET /api/models
│       │   └── useTools.ts     # GET /api/tools
│       └── components/
│           ├── ChatView.tsx    # message list, input, auto-scroll
│           ├── MessageBubble.tsx # markdown rendering, tool call blocks
│           ├── MarkdownCodeBlock.tsx # syntax-highlighted code blocks
│           ├── ToolCallBlock.tsx # inline tool call with status, approval, args/result
│           ├── ModelSelector.tsx
│           ├── ContextMeter.tsx # context-window usage readout in the bar's signal column
│           ├── ToolPanel.tsx   # slide-out tool list with server statuses
│           ├── ConversationList.tsx
│           ├── Toolbar.tsx     # the bar: glyph cluster, model control, signal column
│           ├── icons.tsx       # hand-rolled Material Symbols glyphs for the bar
│           └── SystemPromptEditor.tsx # toolbar-mount glyph, modal
├── docs/
│   ├── current/
│   │   └── pane.md             # design document + built behavior (protocol, event lifecycle, config)
│   └── future/
│       └── roadmap/            # roadmap cards (frontmatter-markdown items, flat)
├── Makefile
├── pane.yaml.example
└── README.md
```

## key design decisions

1. **stateless chat path, disk-backed record** — the backend owns the record (one opaque JSON file per conversation under `data_dir`, the id being the file's name and no field of the body) but keeps no in-memory conversation state: every `/api/chat` request includes the full message history, and the chat path never reads the store. the browser holds a working copy of the estate — state is the working truth, the store is its durable mirror, hydrated on load and persisted on change.

2. **hand-rolled LLM client** — ~150 lines replacing go-openai. clean interfaces so a library can be swapped in later. supports streaming, tool calls, bearer token auth.

3. **ToolExecutor interface** — the tool loop in `internal/llm/toolloop.go` accepts a `ToolExecutor` interface to avoid circular imports between `llm` and `mcp`. `mcp.Manager` satisfies this interface.

4. **model-safe tool names** — MCP tool names are translated to callable names of the form `sanitize(server_tool)` plus a 10-char sha256 suffix, capped at 64 chars (the OpenAI function-name limit). reverse routing uses a registry in `mcp.Manager`, not string splitting. note: `mcp.separator` exists in config and `/api/config` but is currently unused by the name builder.

5. **approval via registry** — tool approvals use a per-tool-call channel registry (`api/approve.go`). the SSE stream blocks on approval; the frontend POSTs to `/api/tools/approve` to unblock.

6. **zrok embed pattern** — frontend source lives in `ui/`, builds to `ui/dist/`, embedded via `//go:embed dist`. `embed_stub.go` with `no_ui` build tag enables headless builds.

7. **explicit model connections** — when `models` is configured, its keys are strict pane-visible aliases. each alias resolves one endpoint, upstream model id, bearer key, context window, and output cap. endpoint and key can inherit the top-level connection; an explicit empty per-model key disables auth. `/api/models` reports the configured aliases without probing hosts. without a registry, the legacy single-endpoint discovery path remains intact.

## API surface

| endpoint | method | description |
|---|---|---|
| `/api/health` | GET | health check |
| `/api/config` | GET | server config (system prompt, model, context windows) |
| `/api/models` | GET | configured aliases in registry mode; proxy to the LLM endpoint's `/v1/models` in legacy mode |
| `/api/chat` | POST | chat completion with MCP tool loop, returns SSE stream |
| `/api/tools` | GET | discovered MCP tools with server statuses |
| `/api/tools/approve` | POST | approve/deny a pending tool call |
| `/api/sessions` | GET | list stored conversation projections (updated descending, id ordinal-ascending) |
| `/api/sessions/{id}` | GET | the session document's body, as stored |
| `/api/sessions/{id}` | PUT | upsert the document under that id |
| `/api/sessions/{id}` | DELETE | delete one conversation |

## SSE streaming protocol

the backend emits typed SSE events: `delta`, `thinking_delta`, `tool_call_start`, `tool_call_args`, `tool_call_executing`, `tool_call_approve`, `tool_call_result`, `usage`, `round_complete`, `error`, `done`. the event data types live in `internal/sse/writer.go`; the frontend state machine that consumes them is in `ui/src/hooks/useChat.ts`. `docs/current/pane.md` documents the full protocol and event lifecycle.

## configuration

config cascade (lowest to highest priority):
1. compiled defaults
2. `~/.config/pane/config.yaml`
3. `./pane.yaml`
4. `--config` flag

loading uses `dd.MergeYAMLFile` with `dd.FileError` not-found handling. see `internal/config/config.go`.

the optional `models` map is keyed by the aliases exposed to the browser. `endpoint` and `api_key` fall back to their top-level values when omitted; `api_key: ""` disables bearer auth for that alias. `upstream_model` defaults to the alias. profile `context_window` and `max_tokens` values are independent of the legacy top-level maps and defaults.

`data_dir` locates the session store: the configured value (a leading `~` expanding to home), else `$XDG_DATA_HOME/pane`, else `~/.local/share/pane`. documents live in a `sessions/` subdirectory under it. the store is always on and fails startup loudly if it cannot be created or written.

## commands

```bash
pane                    # start server (default command)
pane new                # generate pane.yaml in current directory
pane version            # show version
pane --config ./my.yaml # start with explicit config
pane -v                 # verbose logging (debug level)
```

## roadmap

this repo's roadmap lives in `docs/future/roadmap/` — one frontmatter-markdown item per file, per the roadmap convention in the grimoire (software/conventions/roadmap-convention.md). you may add items freely: write the file directly with required `title`, `state: inbox`, and `created:` (today, YYYY-MM-DD), optional `tags`/`source`/`log`, and a body that is a small, clear prompt -- the problem or solution to execute, not documentation of it; trust the code and the day's journal entry for what's discoverable, and point a `log:` stamp at the specific journal entry when a card leans on hard-won context. everything above the first `##` heading is the prompt; supporting material that isn't the prompt goes in named sections below it (`## why` for justification, `## background` for a longer description), which are conventional, never required, and never validated. the filename is the slug of the title (lowercase ascii, hyphens; discard every other character); never overwrite an existing file. read sibling items for the shape.

hard rules: never touch `order.yaml` (priority is the operator's judgment, set at triage); never commit roadmap changes unless directed — the uncommitted diff is the review queue; never delete items; edits change only the lines that express them. label the kind from the house set when one fits: defect, documentation, enhancement, epic, feature, story; add `spike` alongside it when the work carries unknowns that need discovery.

## building

```bash
make build   # npm install + frontend build + go install ./... (default target)
make test    # frontend tests, go test ./... -count=1, and go vet ./...
make clean   # go clean, remove installed binaries, ui/dist, ui/node_modules
```

manually, the same sequence is:

```bash
# frontend (must build before the binary)
cd ui && npm install && npm run build

# binary (embeds frontend)
go install ./...
```

## project rules

1. in Go code, all comments should start with a lowercase letter, unless the first word of the sentence is referring to a Go type that starts with an uppercase letter.

2. all outputs logged or otherwise emitted to a user should prefer lowercase unless it is referring to a type that requires uppercase letters to express accurately. dynamic data in outputs should appear between single quotes, like "the user selected the 'value' setting", where `value` represents a variable.

3. Go files should be named like `dashManager.go` not `dash_manager.go`. unit tests should be named `dashManager_test.go`.

4. never use emoji in code, comments, or output.

5. clean up any build artifacts (binaries, test executables) created during development or testing.

6. the frontend must build before the Go binary. `make build` rebuilds the embedded UI and then installs the binary. `ui/dist/` is gitignored.

7. the LLM client is intentionally hand-rolled. do not add `sashabaranov/go-openai` or similar dependencies without discussion.

8. `dd` struct tags are only needed for `+required`, `+extra`, or name overrides. `dd` converts `CamelCase` to `snake_case` automatically.

## Project memory

Durable knowledge about this project lives in `docs/journal/`, dated files `docs/journal/YYYY-MM-DD.md`. This is project memory; it does not go in harness-local storage (`.claude/` or equivalent), where it's invisible to every other harness and collaborator and dies with the host. Concretely: do not write to your harness's memory directory or memory tool for this project — even when the harness presents it as the default place for durable knowledge. That tool is the silo this convention exists to replace; the journal is the only durable home.

On arrival, read the most recent entries to pick up where the last session left off, before you start changing things. Treat them as prior-session context, not verified truth — if an entry conflicts with the code or a `docs/current/` doc, the code wins.

Write the smallest entry that carries the session's durable insight, and nothing more. The test for every line: *would a competent agent get this wrong, or waste time rediscovering it, working from the tree alone?* If it's recoverable by reading the code, the diff, `docs/current/`, or git history, leave it out.

That filter keeps four kinds of thing and discards the rest:

- **Decisions whose rationale isn't visible in the result** — why a value was chosen, what a line guards against, why something that looks like dead code or a no-op is load-bearing.
- **Deliberate non-actions** — a change you considered and chose not to make, so the next agent doesn't "fix" it. An unchanged file leaves no trace in a diff.
- **Couplings that span files** — two places that must move together, an ordering that matters, an assumption one file makes about another.
- **Live state** — what's unverified, unfinished, or waiting on something external.

Skip change inventories, restatements of the diff, and play-by-play of how you worked. There's no write-time approval gate; Michael reviews on commit. Append to the day's file if it exists, and write the few lines you'd want the next agent to read — honest and self-contained.

## Commits

The operator commits; agents don't. Never run `git commit` or `git push` in this repo. Finish the edit, leave the change in the working tree (staged is fine), report what changed, and hand off — the uncommitted diff is the review queue and the commit is the operator's act of acceptance. Approval of a change is not direction to commit; only an explicit instruction to commit is, and only for that commit.
