// mcpadd is the spike's harmless stdio mcp fixture: one deterministic tool
// that adds two integers. when PANE_SPIKE_EXEC_LOG is set, each execution
// appends one line to that file so tests can count executions. when
// PANE_SPIKE_ADD_DELAY is set (a go duration), the tool records the
// execution first and then waits, to model an effect whose result is lost.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer("pane-spike-add", "0.0.1")
	tool := mcp.NewTool("add",
		mcp.WithDescription("add two integers and return the sum"),
		mcp.WithNumber("a", mcp.Required(), mcp.Description("first integer")),
		mcp.WithNumber("b", mcp.Required(), mcp.Description("second integer")),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, err := req.RequireInt("a")
		if err != nil {
			return mcp.NewToolResultError("a must be an integer"), nil
		}
		b, err := req.RequireInt("b")
		if err != nil {
			return mcp.NewToolResultError("b must be an integer"), nil
		}
		if path := os.Getenv("PANE_SPIKE_EXEC_LOG"); path != "" {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				fmt.Fprintf(f, "add %d %d\n", a, b)
				f.Sync()
				f.Close()
			}
		}
		if d, err := time.ParseDuration(os.Getenv("PANE_SPIKE_ADD_DELAY")); err == nil && d > 0 {
			select {
			case <-time.After(d):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return mcp.NewToolResultText(strconv.Itoa(a + b)), nil
	})
	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintf(os.Stderr, "mcpadd: %v\n", err)
		os.Exit(1)
	}
}
