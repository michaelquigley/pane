# pane

a thin pane of glass between a human and an LLM.

single binary. embedded web UI. first-class MCP stdio support. no docker, no database, no node runtime.

## what it does

- **chat.** a clean web interface for talking to OpenAI-compatible completions endpoints. configured model aliases can select different hosts, credentials, context windows, and output limits. responses stream into disk-backed conversations with markdown rendering and syntax-highlighted code blocks.
- **tools.** spawns MCP servers as local child processes and wires their tools into the chat loop. the LLM calls tools, pane executes them, feeds the results back. same model as Claude Desktop, without the vendor lock-in.
- **approval gates.** per-server human-in-the-loop confirmation before tool execution. see the arguments, approve or deny inline.
- **config, not code.** endpoints, model connections, system prompt, MCP servers — everything lives in a YAML file. `pane new` generates one.

## quick start

```bash
# build (frontend + binary)
make build

# generate a config
pane new

# edit pane.yaml — set your endpoint, model, and MCP servers

# run
pane
```

open `http://localhost:8400` in a browser.

## configuration

pane loads config from (lowest to highest priority):

1. compiled defaults
2. `~/.config/pane/config.yaml`
3. `./pane.yaml`
4. `--config` flag

```yaml
endpoint: http://localhost:11434/v1
api_key: sk-...             # optional bearer token
model: qwen3.8-27b@local
models:
  qwen3.8-27b@local:
    upstream_model: qwen3.8-27b
    context_window: 262144
    max_tokens: 24756
  qwen3.8-27b@remote:
    endpoint: http://model-host:11434/v1
    upstream_model: qwen3.8-27b
    api_key: remote-token
    context_window: 163840
    max_tokens: 24756
system: "You are a helpful assistant."
listen: 127.0.0.1:8400

mcp:
  servers:
    filesystem:
      command: mcp-filesystem
      args:
        - /home/you/projects
      approve: true
      timeout: 30s
```

the `models` keys are the names shown in pane. `endpoint` and `api_key` are optional within each model: an omitted value inherits the top-level setting, while `api_key: ""` intentionally disables bearer authentication for that model. `upstream_model` defaults to the alias. when `models` is omitted, pane keeps the original single-endpoint behavior and discovers models from that endpoint.

the registry also accepts `provider`, `compatibility_profile`, and `reasoning_effort`. `provider: openai-codex` validates subscription model settings and uses credentials managed by `pane auth login openai` (or `--method device`). `pane auth status openai` checks local readiness and `pane auth logout openai` removes pane's local credential. subscription chat sending is not yet enabled; configured aliases currently return an unavailable response. see [the current design record](docs/current/pane.md) for the supported effort values and field rules.

## MCP servers

pane spawns each configured MCP server as a child process and communicates via stdio. it discovers tools at startup, translates them to OpenAI function-calling format, and includes them in every chat request.

tool calls from the LLM are routed back to the appropriate server, executed, and the results are streamed to the browser in real time.

pane generates model-safe callable function names for discovered tools automatically and keeps each original MCP tool name for display and routing.

servers with `approve: true` pause before execution and present the tool name and arguments for human review.

## what pane is not

- **not a model runner.** it doesn't touch GGUF files or GPU memory. that's Ollama's job.
- **not a general gateway.** it selects only explicitly configured model connections. it does no discovery across hosts, load balancing, failover, or multi-user credential management.
- **not multi-tenant.** one human, one browser, one instance.
- **not a framework.** no plugin API, no extension points, no SDK. pane is an appliance.

## license

MIT
