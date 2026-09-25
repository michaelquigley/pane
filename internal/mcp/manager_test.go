package mcp

import (
	"context"
	"errors"
	"testing"

	mcptypes "github.com/mark3labs/mcp-go/mcp"
	"github.com/michaelquigley/pane/internal/llm"
)

func TestManagerDispatchEvidence(t *testing.T) {
	for _, tt := range []struct {
		name    string
		call    func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error)
		ctx     context.Context
		want    llm.Dispatch
		content string
		isError bool
	}{
		{"received", func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
			return &mcptypes.CallToolResult{Content: []mcptypes.Content{mcptypes.TextContent{Text: "ok"}}}, nil
		}, context.Background(), llm.ResultReceived, "ok", false},
		{"tool_error_reply", func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
			return &mcptypes.CallToolResult{IsError: true, Content: []mcptypes.Content{mcptypes.TextContent{Text: "same error"}}}, nil
		}, context.Background(), llm.ResultReceived, "same error", true},
		{"post_call_error", func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
			return nil, errors.New("same error")
		}, context.Background(), llm.UnknownDispatch, "", false},
		{"nil_reply", func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
			return nil, nil
		}, context.Background(), llm.UnknownDispatch, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager := fakeManager(tt.call)
			result := manager.CallTool(tt.ctx, callableToolName("test", "read"), map[string]any{})
			if result.Dispatch != tt.want || result.Content != tt.content || result.IsError != tt.isError {
				t.Fatalf("unexpected execution: %#v", result)
			}
		})
	}

	manager := fakeManager(func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error) {
		t.Fatal("client invoked after cancellation")
		return nil, nil
	})
	if got := manager.CallTool(context.Background(), "missing", nil); got.Dispatch != llm.NotDispatched {
		t.Fatalf("unknown route: %#v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := manager.CallTool(ctx, callableToolName("test", "read"), nil); got.Dispatch != llm.NotDispatched {
		t.Fatalf("cancelled before dispatch: %#v", got)
	}
	manager.servers["test"].status = "error"
	if got := manager.CallTool(context.Background(), callableToolName("test", "read"), nil); got.Dispatch != llm.NotDispatched {
		t.Fatalf("server rejection: %#v", got)
	}
}

func fakeManager(call func(context.Context, mcptypes.CallToolRequest) (*mcptypes.CallToolResult, error)) *Manager {
	manager := NewManager(nil)
	manager.servers["test"] = &ServerInstance{status: "running", callTool: call,
		tools: []mcptypes.Tool{{Name: "read"}}}
	manager.rebuildToolRoutesLocked()
	return manager
}
