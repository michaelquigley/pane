# pane

a thin pane of glass between a human and an LLM.

single binary. embedded web UI. first-class MCP stdio support. no docker, no database, no node runtime.

## what it does

- **chat.** a clean web interface for talking to any OpenAI-compatible completions endpoint. streaming responses, markdown rendering, syntax-highlighted code blocks, conversation history in the browser.
- **tools.** spawns MCP servers as local child processes and wires their tools into the chat loop. the LLM calls tools, pane executes them, feeds the results back. same model as Claude Desktop, without the vendor lock-in.
- **approval gates.** per-server human-in-the-loop confirmation before tool execution. see the arguments, approve or deny inline.
- **config, not code.** endpoint, model, system prompt, MCP servers — everything lives in a YAML file. `pane new` generates one.

## quick start

```bash
# build
cd ui && npm install && npm run build && cd ..
go install ./...

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
model: qwen3.5:35b
system: "You are a helpful assistant."
listen: 127.0.0.1:8400

mcp:
  separator: "_"
  servers:
    filesystem:
      command: mcp-filesystem
      args:
        - /home/you/projects
      approve: true
      timeout: 30s
```

## MCP servers

pane spawns each configured MCP server as a child process and communicates via stdio. it discovers tools at startup, translates them to OpenAI function-calling format, and includes them in every chat request.

tool calls from the LLM are routed back to the appropriate server, executed, and the results are streamed to the browser in real time.

servers with `approve: true` pause before execution and present the tool name and arguments for human review.

## what pane is not

- **not a model runner.** it doesn't touch GGUF files or GPU memory. that's Ollama's job.
- **not a gateway.** it doesn't route between providers or manage API keys across users. point it at your endpoint and go.
- **not multi-tenant.** one human, one browser, one instance.
- **not a framework.** no plugin API, no extension points, no SDK. pane is an appliance.

## license

MIT
