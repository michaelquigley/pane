package main

import (
	"fmt"
	"os"

	"github.com/michaelquigley/df/dl"
	"github.com/spf13/cobra"
)

const configTemplate = `# pane configuration
# config cascade (lowest to highest priority):
#   compiled defaults -> ~/.config/pane/config.yaml -> ./pane.yaml -> --config flag

# OpenAI-compatible endpoint to proxy to
endpoint: http://localhost:18080/v1

# bearer token for LLM endpoint authentication (optional)
#api_key: sk-...

# default model (overridable in UI)
model: qwen2.5:14b

# system prompt (overridable in UI)
system: "You are a helpful assistant."

# listen address
listen: 127.0.0.1:8400

# location of the session store (default shown)
#data_dir: ~/.local/share/pane

# context windows per model id, for the header's context meter.
# models with no entry and no default show '?' in the meter.
#context_windows:
#  qwen2.5:14b: 32768
#default_context_window: 128000

# completion (output) token cap per model id. models with no entry and no
# default use the backend's own output budget. thinking models need one:
# their reasoning consumes the output budget before the answer is produced,
# so a small budget ends the turn with nothing but a token-limit error.
#max_tokens:
#  qwen3.8-27b: 24756
#default_max_tokens: 0

# ask the upstream for token usage on every request (default true).
# set false for an endpoint that rejects the stream_options field.
#include_usage: false

# MCP servers
#mcp:
#  # pane generates model-safe function names automatically.
#  servers:
#    filesystem:
#      command: mcp-filesystem
#      args:
#        - /home/you/projects
#      approve: true
#      timeout: 30s
`

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "new",
		Short: "create a new pane.yaml in the current directory",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			path := "pane.yaml"
			if _, err := os.Stat(path); err == nil {
				dl.Fatalf("%s already exists", path)
			}
			if err := os.WriteFile(path, []byte(configTemplate), 0644); err != nil {
				dl.Fatalf("writing %s: %v", path, err)
			}
			fmt.Printf("wrote %s\n", path)
		},
	})
}
