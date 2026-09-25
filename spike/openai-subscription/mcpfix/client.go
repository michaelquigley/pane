// Package mcpfix connects the harness to the stdio fixture and exposes it as
// the loop's executor.
package mcpfix

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Client is a connected fixture.
type Client struct {
	c     *mcpclient.Client
	tools []round.Tool
}

// Start spawns the fixture binary with the given environment.
func Start(ctx context.Context, binary string, env []string) (*Client, error) {
	c, err := mcpclient.NewStdioMCPClient(binary, env)
	if err != nil {
		return nil, fmt.Errorf("spawning fixture: %w", err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "pane-spike", Version: "0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		c.Close()
		return nil, fmt.Errorf("initializing fixture: %w", err)
	}
	list, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("listing fixture tools: %w", err)
	}
	out := &Client{c: c}
	for _, t := range list.Tools {
		schema, _ := json.Marshal(t.InputSchema)
		out.tools = append(out.tools, round.Tool{Name: t.Name, Description: t.Description, Parameters: schema})
	}
	return out, nil
}

// Tools returns the fixture's tools in model-facing form.
func (c *Client) Tools() []round.Tool { return c.tools }

// Call executes a tool.
func (c *Client) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.c.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	var parts []string
	for _, content := range res.Content {
		if t, ok := content.(mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	text := strings.Join(parts, "\n")
	if res.IsError {
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

// Close stops the fixture.
func (c *Client) Close() error { return c.c.Close() }
