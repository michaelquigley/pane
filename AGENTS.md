# pane

a thin pane of glass between a human and an LLM. Go binary with an embedded React web frontend. first-class MCP stdio support.

## project context

see `README.md` for the user-facing overview. design docs land in `docs/` — `docs/current/` for built behavior, `docs/future/` for forward-looking specs. `docs/current/pane.md` holds the original design document.

pane is a single-binary chat client that proxies to any OpenAI-compatible completions endpoint and spawns local MCP servers as child processes for tool use. conversations live in the browser (localStorage). the backend is stateless.

## tech stack

- **language**: Go 1.26
- **CLI**: github.com/spf13/cobra
- **config**: github.com/michaelquigley/df/dd (YAML binding)
- **logging**: github.com/michaelquigley/df/dl (structured slog wrapper)
- **MCP**: github.com/mark3labs/mcp-go (stdio client)
- **LLM client**: hand-rolled OpenAI-compatible HTTP client (no third-party dependency)
- **frontend**: React 19 + TypeScript + Vite, embedded via go:embed
- **markdown**: react-markdown + remark-gfm + react-syntax-highlighter
- **fonts**: Source Serif 4 + JetBrains Mono, bundled via @fontsource-variable packages (embedded in the binary, no runtime font fetch)

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
│   ├── sse/                    # SSE event writer for pane's streaming protocol
│   │   └── writer.go           # Writer, Send, event data types
│   └── api/                    # HTTP API handlers
│       ├── api.go              # API struct, route registration
│       ├── chat.go             # POST /api/chat (tool loop integration)
│       ├── models.go           # GET /api/models
│       ├── tools.go            # GET /api/tools
│       └── approve.go          # POST /api/tools/approve, ApprovalRegistry
├── ui/                         # frontend source + embed (zrok pattern)
│   ├── embed.go                # //go:embed dist (build tag: !no_ui)
│   ├── embed_stub.go           # empty FS (build tag: no_ui)
│   ├── middleware.go           # SPA middleware: /api/ passthrough, index.html fallback
│   └── src/
│       ├── App.tsx             # top-level layout, conversation state (localStorage)
│       ├── types.ts            # Conversation, Message, ToolCall, SSEEvent, etc.
│       ├── lib/
│       │   ├── sse.ts          # SSE stream parser (pane's protocol)
│       │   └── exportMarkdown.ts # conversation-to-markdown export
│       ├── hooks/
│       │   ├── useChat.ts      # SSE streaming, tool call state machine, approvals
│       │   ├── useConfig.ts    # GET /api/config
│       │   ├── useLocalStorage.ts
│       │   ├── useModels.ts    # GET /api/models
│       │   └── useTools.ts     # GET /api/tools
│       └── components/
│           ├── ChatView.tsx    # message list, input, auto-scroll
│           ├── MessageBubble.tsx # markdown rendering, tool call blocks
│           ├── MarkdownCodeBlock.tsx # syntax-highlighted code blocks
│           ├── ToolCallBlock.tsx # inline tool call with status, approval, args/result
│           ├── ModelSelector.tsx
│           ├── ToolPanel.tsx   # slide-out tool list with server statuses
│           ├── ConversationList.tsx
│           └── SystemPromptEditor.tsx
├── docs/
│   └── current/
│       └── pane.md             # original design document
├── Makefile
├── pane.yaml.example
└── README.md
```

## key design decisions

1. **stateless backend** — the browser owns conversation state (localStorage). the backend is a proxy with MCP superpowers. every chat request includes the full message history.

2. **hand-rolled LLM client** — ~150 lines replacing go-openai. clean interfaces so a library can be swapped in later. supports streaming, tool calls, bearer token auth.

3. **ToolExecutor interface** — the tool loop in `internal/llm/toolloop.go` accepts a `ToolExecutor` interface to avoid circular imports between `llm` and `mcp`. `mcp.Manager` satisfies this interface.

4. **model-safe tool names** — MCP tool names are translated to callable names of the form `sanitize(server_tool)` plus a 10-char sha256 suffix, capped at 64 chars (the OpenAI function-name limit). reverse routing uses a registry in `mcp.Manager`, not string splitting. note: `mcp.separator` exists in config and `/api/config` but is currently unused by the name builder.

5. **approval via registry** — tool approvals use a per-tool-call channel registry (`api/approve.go`). the SSE stream blocks on approval; the frontend POSTs to `/api/tools/approve` to unblock.

6. **zrok embed pattern** — frontend source lives in `ui/`, builds to `ui/dist/`, embedded via `//go:embed dist`. `embed_stub.go` with `no_ui` build tag enables headless builds.

## API surface

| endpoint | method | description |
|---|---|---|
| `/api/health` | GET | health check |
| `/api/config` | GET | server config (system prompt, default model) |
| `/api/models` | GET | proxy to LLM endpoint's /v1/models |
| `/api/chat` | POST | chat completion with MCP tool loop, returns SSE stream |
| `/api/tools` | GET | discovered MCP tools with server statuses |
| `/api/tools/approve` | POST | approve/deny a pending tool call |

## SSE streaming protocol

the backend emits typed SSE events: `delta`, `tool_call_start`, `tool_call_args`, `tool_call_executing`, `tool_call_approve`, `tool_call_result`, `round_complete`, `error`, `done`. the event data types live in `internal/sse/writer.go`; the frontend state machine that consumes them is in `ui/src/hooks/useChat.ts`. `docs/current/pane.md` documents the full protocol and event lifecycle.

## configuration

config cascade (lowest to highest priority):
1. compiled defaults
2. `~/.config/pane/config.yaml`
3. `./pane.yaml`
4. `--config` flag

loading uses `dd.MergeYAMLFile` with `dd.FileError` not-found handling. see `internal/config/config.go`.

## commands

```bash
pane                    # start server (default command)
pane new                # generate pane.yaml in current directory
pane version            # show version
pane --config ./my.yaml # start with explicit config
pane -v                 # verbose logging (debug level)
```

## building

```bash
make build   # npm install + frontend build + go install ./... (default target)
make test    # go test ./... -count=1 && go vet ./...
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
