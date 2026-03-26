# pane — implementation plan

a thin pane of glass between a human and an LLM. Go binary with an embedded web frontend. first-class MCP stdio support.

design document: `~/Repos/q/writing/grimoire/software/pane/pane-design-document.md`

## conventions

pane follows the patterns established in frame, lore, and df:

- **module path:** `github.com/michaelquigley/pane`
- **CLI:** cobra with `cmd/pane/main.go` entry point. `rootCmd` runs the server. subcommands registered via `init()` in separate files.
- **logging:** `dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/michaelquigley/"))` in `init()`. `dl.Fatalf` for fatal CLI errors. `dl.Infof` / `dl.Debugf` for operational messages.
- **config:** `internal/config/` package. structs with `dd` tags. `dd.MergeYAMLFile` with `dd.FileError` not-found handling. XDG config path via `$XDG_CONFIG_HOME/pane/config.yaml` fallback `~/.config/pane/config.yaml`. default config function.
- **packages:** `internal/` for non-exported packages (config, mcp, llm, api, sse).
- **errors:** `fmt.Errorf("context: %w", err)`. no custom error types unless the caller needs to distinguish cases.
- **signals:** `context.WithCancel` + goroutine on `SIGINT`/`SIGTERM` (same pattern as lore/serve.go).
- **embed:** zrok pattern. frontend source lives inside `ui/` package. `//go:embed dist` in `ui/embed.go`, `embed_stub.go` with `no_ui` build tag for headless builds. SPA middleware adapted from zrok/archive.
- **namespace separator:** `_` by default (e.g., `baabhive_hive_sql`). configurable via `mcp.separator` in config.
- **build:** simple version command (no `push/build` dependency — it's still private). hardcoded dev version for now, add `push/build` later when it's public.

## file tree

```
pane/
  go.mod
  go.sum
  cmd/pane/
    main.go                     # cobra root (runs server), verbose flag, version subcommand
    version.go                  # version command (push/build pattern)
  internal/
    config/
      config.go                 # Config, MCPConfig, ServerConfig structs + Load()
    mcp/
      manager.go                # spawn/init/discover/execute MCP servers
      tool.go                   # ToolInfo type, MCP-to-OpenAI translation, namespace parsing
    llm/
      client.go                 # hand-rolled OpenAI-compatible HTTP client
      types.go                  # ChatRequest, Message, Tool, ToolCall, StreamChunk, etc.
      stream.go                 # SSE stream reader (parses OpenAI streaming format)
      toolloop.go               # tool-call loop: stream -> execute -> re-send
    sse/
      writer.go                 # SSE event types + http.ResponseWriter wrapper
    api/
      api.go                    # API struct, NewAPI(), route registration
      chat.go                   # POST /api/chat
      models.go                 # GET /api/models
      tools.go                  # GET /api/tools, POST /api/tools/toggle
      approve.go                # POST /api/tools/approve + pending request registry
  ui/                               # frontend source + embed (zrok pattern)
    embed.go                        # //go:embed dist
    embed_stub.go                   # build tag no_ui: empty embed.FS
    middleware.go                   # SPA middleware (adapted from zrok)
    package.json
    vite.config.ts
    tsconfig.json
    index.html
    src/
      main.tsx
      App.tsx
      index.css
      types.ts
      hooks/
        useChat.ts              # SSE lifecycle, tool call state machine
        useLocalStorage.ts      # generic localStorage hook
        useTools.ts             # GET /api/tools, toggle
        useModels.ts            # GET /api/models
      components/
        ChatView.tsx            # message list + input area
        MessageBubble.tsx       # single message with markdown rendering
        ToolCallBlock.tsx       # inline tool call display with states
        ModelSelector.tsx       # dropdown from /api/models
        ToolPanel.tsx           # slide-out sidebar with tool toggles
        ConversationList.tsx    # sidebar conversation list
        SystemPromptEditor.tsx  # editable system prompt
    dist/                       # vite build output (gitignored)
  .gitignore                    # ui/dist/, ui/node_modules/, build/
  pane.yaml.example             # annotated example config
  docs/
    PANE.md                     # this file
```

## reference patterns

concrete examples from sibling projects so a fresh agent doesn't have to explore.

**version constant** — lives in `internal/config/config.go` so both `cmd/pane/` and `internal/mcp/` can import it:
```go
const Version = "v0.0.x-dev"
```

**version command** (`cmd/pane/version.go` — simple, no `push/build` dependency for now):
```go
package main

import (
	"fmt"
	"runtime"

	"github.com/michaelquigley/pane/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "show version information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("pane %s (%s/%s)\n", config.Version, runtime.GOOS, runtime.GOARCH)
		},
	})
}
```
when `push/build` is made public, swap this for `build.NewVersionCmd("pane")` and use `build.String()` for the MCP client version field.

**config loading** (from lore `internal/config/load.go`):
```go
func Load(configPath string) (*Config, error) {
    cfg := DefaultConfig()
    // cascade: each MergeYAMLFile overwrites fields from previous layer
    // so later layers have higher priority
    if err := mergeIfExists(cfg, globalConfigPath()); err != nil {
        return nil, err
    }
    if err := mergeIfExists(cfg, "./pane.yaml"); err != nil {
        return nil, err
    }
    if configPath != "" {
        if err := dd.MergeYAMLFile(cfg, configPath); err != nil {
            return nil, err
        }
    }
    return cfg, nil
}

func mergeIfExists(cfg *Config, path string) error {
    err := dd.MergeYAMLFile(cfg, path)
    if err != nil {
        var fileErr *dd.FileError
        if errors.As(err, &fileErr) && fileErr.IsNotFound() {
            return nil
        }
        return err
    }
    return nil
}
```

**SPA middleware** (from zrok `ui/middleware.go` — adapt `/api/v2` to `/api/`):
```go
func Middleware(handler http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/api/") {
            handler.ServeHTTP(w, r)
            return
        }
        // ... serve from embed.FS with index.html SPA fallback
    })
}
```

**signal handling** (from lore `cmd/lore/serve.go`):
```go
ctx, cancel := context.WithCancel(context.Background())
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
go func() {
    <-sigCh
    cancel()
}()
```

**OpenAI streaming format** (critical for hand-rolled client):
- response is `Content-Type: text/event-stream`
- each chunk: `data: {"id":"...","choices":[{"index":0,"delta":{"content":"token"},"finish_reason":null}]}\n\n`
- tool calls arrive as: `delta.tool_calls` array where each entry has an `index` field (int) identifying which tool call it belongs to. first chunk for a tool call has `id` and `function.name`; subsequent chunks append to `function.arguments`.
- stream ends with `data: [DONE]\n\n`
- `finish_reason` is `"stop"` for content completion, `"tool_calls"` when tool calls are present

## phases

### phase 1 — skeleton

**goal:** `go build` produces a binary that loads YAML config, inits logging, serves a stub page on :8400.

**files:**

`go.mod` — module `github.com/michaelquigley/pane`, go 1.26, require `github.com/michaelquigley/df`, `github.com/spf13/cobra`.

`cmd/pane/main.go` — cobra root command. pattern:
```go
var verbose bool
var configPath string
var rootCmd = &cobra.Command{
    Use:   "pane",
    Short: "pane - a thin pane of glass between a human and an LLM",
    Run:   run,
}

func init() {
    dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/michaelquigley/"))
    rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
    rootCmd.Flags().StringVar(&configPath, "config", "", "config file path")
    rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
        if verbose {
            dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/michaelquigley/").SetLevel(slog.LevelDebug))
        }
    }
}
```

`run` function: load config via cascade, create `http.ServeMux`, mount `ui.Middleware(mux)`, add `GET /api/health`, `http.ListenAndServe(cfg.Listen, handler)`.

`cmd/pane/version.go` — simple version subcommand (see reference patterns section). no `push/build` dependency yet.

`internal/config/config.go` — structs:
```go
type Config struct {
    Endpoint string
    Model    string
    System   string
    Listen   string
    MCP      *MCPConfig
}

type MCPConfig struct {
    Servers   map[string]*ServerConfig
    Separator string                     // tool namespace separator (default "_")
}

type ServerConfig struct {
    Command string            `dd:",+required"`
    Args    []string
    Env     map[string]string
    Approve bool
    Timeout string
}
```

`DefaultConfig()` returns: endpoint `http://localhost:18080/v1`, model `qwen2.5:14b`, system `"You are a helpful assistant."`, listen `127.0.0.1:8400`, MCP separator `"_"`.

`Load(configPath string) (*Config, error)` — cascade (lowest to highest priority): compiled defaults -> `~/.config/pane/config.yaml` -> `./pane.yaml` -> explicit `--config` path. each layer via `dd.MergeYAMLFile` (later overwrites earlier). missing files silently ignored via `dd.FileError`. see reference patterns section for implementation.

`ui/embed.go` (zrok pattern — build-tagged so headless builds work):
```go
//go:build !no_ui

package ui

import "embed"

//go:embed dist
var FS embed.FS
```

`ui/embed_stub.go`:
```go
//go:build no_ui

package ui

import "embed"

var FS embed.FS
```

`ui/middleware.go` — adapted from zrok. check `/api/` prefix (passthrough to handler), everything else serves from embedded FS with `index.html` SPA fallback.

`ui/` also contains the frontend source (zrok pattern — source and embed live in the same package). scaffold Vite + React + TypeScript inside `ui/`. `vite.config.ts`:
- default `build.outDir` = `"dist"` (relative to `ui/`, so `//go:embed dist` works)
- dev proxy: `/api` -> `http://127.0.0.1:8400`

stub `App.tsx` renders "pane" centered.

`Makefile`:
```makefile
.PHONY: frontend build dev clean

frontend:
	cd ui && npm install && npm run build

build: frontend
	go build -o build/pane ./cmd/pane

dev:
	cd ui && npm run dev &
	go run ./cmd/pane --config ./pane.yaml

clean:
	rm -rf build/ ui/dist/
```

**verify:**
- `make build` succeeds
- `./build/pane` logs listen address
- `curl localhost:8400/` returns HTML
- `curl localhost:8400/api/health` returns `{"status":"ok"}`
- config override works: `listen: 127.0.0.1:9000` in `./pane.yaml`

---

### phase 2 — chat proxy with SSE streaming (no tools)

**goal:** `POST /api/chat` proxies to llm-gateway and streams back SSE events. `GET /api/models` returns models.

**files:**

`internal/sse/writer.go`:
- `Writer` struct wrapping `http.ResponseWriter` + `http.Flusher`
- `NewWriter(w http.ResponseWriter) (*Writer, error)` — sets `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`
- `Send(eventType string, data any) error` — writes `event: {type}\ndata: {json}\n\n`, flushes
- event data structs:
  - `DeltaData{Content string}`
  - `ErrorData{Code string, Message string, ToolCallID string}`
  - `ToolCallStartData{ID string, Name string}`
  - `ToolCallArgsData{ID string, ArgumentsPartial string}`
  - `ToolCallExecutingData{ID string, Name string}`
  - `ToolCallApproveData{ID string, Name string, Arguments string}`
  - `ToolCallResultData{ID string, Name string, Content string, DurationMS int64}`

`internal/llm/client.go` — hand-rolled OpenAI-compatible client (no third-party dependency):
- `Client` struct holding `*http.Client`, `baseURL string`, `defaultModel string`
- `NewClient(endpoint, model string) *Client` — stores endpoint as base URL
- `ListModels(ctx) (*ModelsResponse, error)` — `GET {baseURL}/models`, decode JSON
- `StreamChat(ctx, req *ChatRequest) (*StreamReader, error)` — `POST {baseURL}/chat/completions` with `stream: true`, returns `StreamReader` wrapping the response body

`internal/llm/types.go` — OpenAI-compatible types (only what pane needs):
- `ChatRequest{Model, Messages, Tools, Stream bool}`
- `Message{Role, Content, ToolCalls, ToolCallID}`
- `Tool{Type, Function}`, `FunctionDef{Name, Description, Parameters}`
- `ToolCall{ID, Type, Function{Name, Arguments}}`
- `StreamChunk{Choices []Choice}`, `Choice{Delta, FinishReason}`
- `Delta{Content, ToolCalls}`
- `ModelsResponse{Data []Model}`, `Model{ID, OwnedBy}`

`internal/llm/stream.go` — SSE stream reader:
- `StreamReader` wraps `*bufio.Reader` over the HTTP response body
- `Recv() (*StreamChunk, error)` — reads `data: ` lines, skips `[DONE]`, decodes JSON
- `Close()` — closes response body

this is ~150 lines of code replacing a large dependency. the interface is clean enough that swapping in a library later is trivial.

`internal/api/api.go`:
- `API` struct holding `*llm.Client`, `*config.Config`
- `NewAPI(cfg *config.Config, llmClient *llm.Client) *API`
- `RegisterRoutes(mux *http.ServeMux)` — wires all handlers

`internal/api/chat.go` — `POST /api/chat`:
- parse JSON body: `model`, `messages` (OpenAI format), `tools_disabled`
- prepend system prompt if messages[0] is not role "system"
- create SSE writer
- call `llm.StreamChat`, loop on `stream.Recv()`:
  - `Delta.Content` non-empty -> emit `delta`
  - `Recv` returns `io.EOF` -> emit `done`
  - error -> emit `error` with `upstream_error` code
- no tool handling yet

`internal/api/models.go` — `GET /api/models`:
- call `llm.ListModels(ctx)`
- return JSON (passthrough OpenAI format)

`cmd/pane/main.go` — updated `run`: create `llm.Client`, create `API`, register routes on mux.

**verify:**
- `curl -N -X POST localhost:8400/api/chat -H 'Content-Type: application/json' -d '{"messages":[{"role":"user","content":"Hello"}]}'` — streams `delta` events, ends with `done`
- `curl localhost:8400/api/models` — returns model list
- llm-gateway down: `error` event with `upstream_unreachable`
- multi-turn conversation works (3+ messages)

---

### phase 3 — MCP server manager and tool discovery

**goal:** spawn MCP stdio servers on startup, discover tools, translate to OpenAI format. expose via API.

**files:**

`go.mod` — add `github.com/mark3labs/mcp-go`.

`internal/mcp/manager.go`:
```go
type Manager struct {
    servers map[string]*ServerInstance
    mu      sync.RWMutex
}

type ServerInstance struct {
    Config     *config.ServerConfig
    client     *mcpclient.Client  // mcp-go client
    tools      []mcp.Tool         // discovered MCP tools
    status     string             // "starting", "running", "error"
    err        string             // error message if status == "error"
    disabled   map[string]bool    // tool name -> disabled
}
```

- `NewManager(cfg *config.MCPConfig) *Manager`
- `Start(ctx context.Context) error` — for each configured server:
  - convert `map[string]string` env to `[]string{"K=V"}`
  - `mcpclient.NewStdioMCPClient(command, env, args...)`
  - `client.Initialize(ctx, initRequest)` with `{Name: "pane", Version: config.Version}`
  - `client.ListTools(ctx, mcp.ListToolsRequest{})`
  - log per-server: tools discovered or error. failures don't crash pane.
- `Stop()` — close all clients (kills child processes)
- `GetServer(name string) *ServerInstance`
- `GetAllTools() []ToolInfo` — thread-safe read across all servers
- `GetEnabledTools(disabled []string) []llm.Tool` — translated to OpenAI format, filtered by both toggle state and request-level `tools_disabled`
- `CallTool(ctx, serverName, toolName string, args map[string]any) (string, time.Duration, error)` — dispatch to server's MCP client, apply per-server timeout (parsed from config), extract text from result `Content` array, return duration

`internal/mcp/tool.go`:
```go
type ToolInfo struct {
    Server      string              `json:"server"`
    Enabled     bool                `json:"enabled"`
    Function    ToolFunction        `json:"function"`
}

type ToolFunction struct {
    Name        string              `json:"name"`        // namespaced "server_tool"
    Description string              `json:"description"`
    Parameters  json.RawMessage     `json:"parameters"`  // JSON Schema passthrough
}
```

- `TranslateToOpenAI(info ToolInfo) llm.Tool` — `Name` -> `function.name`, `Description` -> `function.description`, `Parameters` -> `function.parameters`
- `ParseToolName(qualified, separator string) (serverName, toolName string)` — split on first occurrence of separator
- `QualifyToolName(server, tool, separator string) string` — join with separator

`internal/api/tools.go`:
- `GET /api/tools` — returns `{"tools": [...], "servers": {...}}` per design doc schema
- `POST /api/tools/toggle` — body `{"tool": "server_tool", "enabled": false}`, updates manager toggle state. returns 204.

`internal/api/api.go` — updated: `API` struct gains `*mcp.Manager`. `RegisterRoutes` wires tool endpoints.

`cmd/pane/main.go` — updated `run`: create `mcp.Manager`, `Start(ctx)`, defer `Stop()`, pass to API.

**verify:**
- configure filesystem MCP server in `pane.yaml`. logs show tool discovery.
- `curl localhost:8400/api/tools` returns tools with schemas and server statuses
- toggle a tool, verify `enabled: false` in subsequent GET
- bad MCP config: pane starts, server shows `status: "error"`

---

### phase 4 — tool-call loop

**goal:** when LLM returns `tool_calls`, execute via MCP, stream intermediate events, re-send. support approval gates.

**files:**

`internal/llm/toolloop.go` — core loop. to avoid a circular dependency between `llm` and `mcp`, the tool loop accepts a `ToolExecutor` interface rather than importing `mcp.Manager` directly:
```go
// in internal/llm/toolloop.go
type ToolExecutor interface {
    CallTool(ctx context.Context, qualifiedName string, args map[string]any) (content string, duration time.Duration, err error)
    NeedsApproval(qualifiedName string) bool
}
```
`mcp.Manager` satisfies this interface. the `api` layer wires them together.

```
RunToolLoop(ctx, llmClient, messages, model, tools, executor ToolExecutor, sseWriter, approvalCh) error

loop (max 20 iterations):
  stream = llmClient.StreamChat(ctx, {messages, model, tools})
  assistantMsg = {role: "assistant", content: "", tool_calls: []}
  pendingToolCalls = []

  for chunk in stream.Recv():
    if Delta.Content != "":
      assistantMsg.Content += Delta.Content
      emit("delta", {content})
    if Delta.ToolCalls != nil:
      for each tc (matched by Index):
        if new index:
          emit("tool_call_start", {id, name})
          create pending entry
        pending.args += tc.Function.Arguments
        emit("tool_call_args", {id, arguments_partial})

  append assistantMsg (with ToolCalls) to messages
  if no pendingToolCalls:
    emit("done", {})
    return

  // execute tool calls (concurrent via goroutines for multiple)
  for each toolCall:
    if executor.NeedsApproval(toolCall.Name):
      emit("tool_call_approve", {id, name, arguments})
      block on approvalCh (5min timeout)
      if denied:
        result = "Tool call denied by user"
        emit("tool_call_result", {id, name, content: result, duration_ms: 0})
        append tool message to messages
        continue

    emit("tool_call_executing", {id, name})
    result, duration, err = executor.CallTool(ctx, toolCall.Name, parsedArgs)
    if err:
      result = "Error: " + err.Error()
    emit("tool_call_result", {id, name, content: result, duration_ms})
    append {role: "tool", tool_call_id: id, content: result} to messages

  continue loop
```

`internal/api/approve.go`:
- `RequestRegistry` struct: `map[string]chan ApprovalResponse`, `sync.Mutex`
- `ApprovalResponse{ToolCallID string, Approved bool}`
- `Register(requestID string) <-chan ApprovalResponse` — creates buffered channel
- `Submit(requestID, toolCallID string, approved bool) error`
- `Unregister(requestID string)`
- `POST /api/tools/approve` handler: body `{"id": "tc_1", "approved": true}`. looks up active request by a header or query param, sends on channel.

`internal/api/chat.go` — updated:
- generate request ID (nanoid or uuid)
- register in approval registry, defer unregister
- call `RunToolLoop` instead of raw `StreamChat`
- pass `mcpMgr.GetEnabledTools(req.ToolsDisabled)` as tools
- pass approval channel

**design note on approval routing:** the SSE stream is per-request, so the frontend knows which request it's listening to. the approval POST needs to identify which pending request to unblock. options:
1. emit the request ID in the first SSE event, frontend includes it in approval POST
2. use the tool_call ID directly as the lookup key (simpler — each tool_call ID is unique within the process lifetime)

recommend option 2: the registry maps `tool_call_id -> chan bool`. simpler, no request ID needed.

**verify:**
- ask LLM to list files via filesystem MCP: full SSE lifecycle (delta -> tool_call_start -> tool_call_args -> tool_call_executing -> tool_call_result -> delta -> done)
- `approve: true`: stream shows `tool_call_approve`, hangs until POST
- denial: LLM receives "Tool call denied by user", responds gracefully
- MCP error: error injected as tool result
- max iterations: doesn't loop forever (cap at 20)
- multiple tool calls in one turn: executed concurrently, results emitted as they complete

---

### phase 5 — frontend

**goal:** complete React UI per design doc.

**dependencies:** `react-markdown`, `remark-gfm`, `react-syntax-highlighter` (with `prism`), `nanoid`

**`ui/src/types.ts`** — all types from design doc:
- `Conversation` (id, title, messages, createdAt, updatedAt)
- `Message` (role, content, toolCalls?, toolCallId?)
- `ToolCall` (id, type, function: {name, arguments})
- `SSEEvent` — discriminated union of all event types
- `ToolInfo`, `ServerStatus`
- `ActiveToolCall` — extends ToolCall with `status` (loading | args_streaming | awaiting_approval | executing | complete | error), `result?`, `durationMs?`

**`ui/src/hooks/useLocalStorage.ts`** — generic `useLocalStorage<T>(key, defaultValue)` hook.

**`ui/src/hooks/useChat.ts`** — the core hook:
- state: `messages`, `isStreaming`, `activeToolCalls` (Map<id, ActiveToolCall>), `streamingContent`, `error`
- `sendMessage(content: string)`:
  - append user message
  - `fetch('/api/chat', {method: 'POST', body: {model, messages, tools_disabled}, signal: abortController.signal})`
  - read response via `getReader()`, parse SSE lines manually (not EventSource — need POST body)
  - on each event type: update state per the state machine in design doc
  - on `done`: finalize assistant message (including any tool_calls and intervening tool results), save to conversation
- `approveToolCall(toolCallId)` / `denyToolCall(toolCallId)` — `POST /api/tools/approve`
- `abort()` — cancel in-flight request via AbortController

**`ui/src/hooks/useTools.ts`** — `GET /api/tools` on mount, `toggleTool(name, enabled)` via POST.

**`ui/src/hooks/useModels.ts`** — `GET /api/models` on mount.

**`ui/src/App.tsx`** — layout:
- collapsible sidebar (ConversationList)
- main area: header bar (ModelSelector, system prompt toggle, tool panel toggle) + ChatView
- state: selected conversation ID, conversations list (from localStorage)

**`ui/src/components/ChatView.tsx`**:
- centered column, `max-width: 720px`
- message list with auto-scroll
- input: textarea at bottom, Enter to send, Shift+Enter for newline
- send button disabled while streaming
- typing indicator (pulsing dot) while waiting for first delta

**`ui/src/components/MessageBubble.tsx`**:
- `react-markdown` + `remark-gfm` for assistant content
- `react-syntax-highlighter` with Prism for code blocks
- if message has `tool_calls`: render `ToolCallBlock` for each, inline in the message flow
- user messages: right-aligned or visually distinct
- streaming cursor at end of assistant message while streaming

**`ui/src/components/ToolCallBlock.tsx`**:
- inline block: slightly inset, muted background
- header: tool name (monospace), status indicator
- collapsible arguments (JSON, monospace, syntax-highlighted)
- collapsible result (monospace)
- states: loading (shimmer), args_streaming (args building), awaiting_approval (approve/deny buttons), executing (spinner), complete (checkmark), error (red)
- approve/deny buttons call `useChat.approveToolCall` / `denyToolCall`

**`ui/src/components/ModelSelector.tsx`**:
- dropdown populated from `useModels`
- selected model persisted in localStorage

**`ui/src/components/ToolPanel.tsx`**:
- slide-out sidebar or collapsible panel
- tools grouped by server
- server status dot (green = running, red = error)
- toggle switch per tool

**`ui/src/components/ConversationList.tsx`**:
- list from localStorage
- new conversation button
- delete conversation (with confirmation)
- title: first user message, truncated to ~50 chars
- active conversation highlighted

**`ui/src/components/SystemPromptEditor.tsx`**:
- small textarea, collapsible
- persisted per-conversation in localStorage
- overrides config system prompt when set

**`ui/src/index.css`**:
- `@import` PT Serif from Google Fonts
- CSS custom properties for theming:
  ```css
  :root {
    --bg: #faf8f5;        /* warm cream */
    --fg: #2d2a26;        /* warm charcoal */
    --bg-muted: #f0ede8;  /* tool blocks, sidebar */
    --accent: #c4841d;    /* warm amber */
    --mono: 'JetBrains Mono', ui-monospace, monospace;
    --serif: 'PT Serif', Georgia, serif;
  }
  @media (prefers-color-scheme: dark) {
    :root {
      --bg: #1e1c1a;
      --fg: #e8e4df;
      --bg-muted: #2a2826;
      --accent: #d4943d;
    }
  }
  ```
- body: `font-family: var(--serif)`. code: `font-family: var(--mono)`.
- generous whitespace, comfortable line-height (~1.6)
- subtle transitions, no animations beyond a pulsing dot for streaming

**verify:**
- full flow in browser: create conversation, send message, streaming response renders
- conversations persist across reload
- model selector works, persists selection
- tool panel shows tools, toggles work
- tool calls render inline: args collapsible, result collapsible
- approval flow: buttons appear, clicking approve continues stream
- dark mode follows system preference
- code blocks highlighted
- markdown renders correctly (headers, lists, links, tables)

---

### phase 6 — polish and error handling

**goal:** production hardening, graceful shutdown, reconnection, edge cases.

`cmd/pane/main.go` — graceful shutdown:
- `context.WithCancel` on SIGINT/SIGTERM (same pattern as lore/serve.go)
- on signal: stop MCP manager, drain in-flight requests (use `http.Server.Shutdown`), exit
- pass cancellable context to MCP manager start

`internal/mcp/manager.go` — crash detection:
- monitor MCP client for broken pipe / process exit
- set status to "error", log
- attempt one restart after 1s delay

`internal/api/chat.go` — client disconnect:
- use `r.Context()` (cancelled on client disconnect)
- propagate to LLM stream and MCP calls via context

`internal/llm/toolloop.go` — edge cases:
- malformed JSON args from LLM: return parse error as tool result text
- tool call to dead server: return error as tool result
- tool call to disabled tool: return "tool is disabled" as result
- empty tool_calls array: treat as no tools, emit done

`internal/config/config.go` — validation after load:
- endpoint is non-empty
- listen is valid `host:port`
- timeout strings parse to `time.Duration`
- server command is non-empty

frontend robustness:
- `AbortController` cancels in-flight request on new message or conversation switch
- SSE connection drop shows "connection lost" with retry button
- typing indicator while waiting for first delta after send
- localStorage quota error: catch, show warning, offer to delete old conversations

**verify:**
- Ctrl+C: clean shutdown, MCP processes killed, logs "shutting down"
- kill MCP server process mid-conversation: error in tool result, LLM handles gracefully
- browser disconnect mid-stream: backend cancels LLM request (check logs)
- bad llm-gateway URL: clear error in UI
- very long conversation: localStorage handles it (or shows quota warning)

---

## phase dependencies

```
phase 1 (skeleton)
    |
    +---> phase 2 (chat proxy)    phase 3 (MCP manager)
    |          |                        |
    |          +------------------------+
    |                    |
    |             phase 4 (tool loop)
    |                    |
    +---> phase 5 (frontend) --- basic chat after phase 2
    |                            tool UI after phase 4
    |                    |
    +--------------------+
              |
       phase 6 (polish)
```

phases 2 and 3 can be built in parallel after phase 1. phase 4 requires both. phase 5 frontend work can start after phase 2 (basic chat UI) and gain tool call UI after phase 4 lands. phase 6 is last.

## resolved questions

1. ~~**module path.**~~ `github.com/michaelquigley/pane`.
2. ~~**cobra vs bare flags.**~~ cobra. `--config` overrides default `~/.config/pane/config.yaml`.
3. ~~**namespace separator.**~~ `_` by default, configurable via `mcp.separator`.
4. ~~**go-openai vs raw HTTP.**~~ hand-rolled client behind clean interfaces. ~150 lines, no third-party dependency. swappable later.
5. ~~**frontend layout.**~~ zrok pattern: source + embed in `ui/` package. `embed_stub.go` with `no_ui` build tag.
