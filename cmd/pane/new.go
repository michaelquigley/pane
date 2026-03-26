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

# default model (overridable in UI)
model: qwen2.5:14b

# system prompt (overridable in UI)
system: "You are a helpful assistant."

# listen address
listen: 127.0.0.1:8400

# MCP servers
#mcp:
#  separator: "_"
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
