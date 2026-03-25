package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/sse"
)

const maxToolLoopIterations = 20

// ToolExecutor abstracts MCP tool execution to avoid circular imports.
type ToolExecutor interface {
	CallTool(ctx context.Context, qualifiedName string, args map[string]any) (string, time.Duration, error)
	NeedsApproval(qualifiedName string) bool
}

// ApprovalRegistry manages pending tool call approvals.
type ApprovalRegistry interface {
	Register(toolCallID string) <-chan bool
	Unregister(toolCallID string)
}

// pendingToolCall tracks a tool call being accumulated from streaming chunks.
type pendingToolCall struct {
	ID        string
	Name      string
	Arguments string
	Index     int
}

// RunToolLoop runs the full chat-with-tools loop: stream LLM response, execute
// tool calls via MCP, append results, re-send until the LLM produces a final
// content-only response.
func RunToolLoop(
	ctx context.Context,
	client *Client,
	messages []Message,
	model string,
	tools []Tool,
	executor ToolExecutor,
	sw *sse.Writer,
	approvals ApprovalRegistry,
) error {
	for iteration := 0; iteration < maxToolLoopIterations; iteration++ {
		req := &ChatRequest{
			Model:    model,
			Messages: messages,
		}
		if len(tools) > 0 {
			req.Tools = tools
		}

		stream, err := client.StreamChat(ctx, req)
		if err != nil {
			code := "upstream_error"
			if strings.Contains(err.Error(), "unreachable") {
				code = "upstream_unreachable"
			}
			_ = sw.Send("error", sse.ErrorData{Code: code, Message: err.Error()})
			return err
		}

		// accumulate the assistant response
		var contentBuf strings.Builder
		pending := make(map[int]*pendingToolCall)
		var streamErr error

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				streamErr = err
				break
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			delta := chunk.Choices[0].Delta

			// content tokens
			if delta.Content != nil && *delta.Content != "" {
				contentBuf.WriteString(*delta.Content)
				_ = sw.Send("delta", sse.DeltaData{Content: *delta.Content})
			}

			// tool call tokens
			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}

				existing, ok := pending[idx]
				if !ok {
					// new tool call
					existing = &pendingToolCall{
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Index: idx,
					}
					pending[idx] = existing
					if tc.ID != "" && tc.Function.Name != "" {
						_ = sw.Send("tool_call_start", sse.ToolCallStartData{
							ID:   tc.ID,
							Name: tc.Function.Name,
						})
					}
				}

				// accumulate ID/name if they arrive in later chunks
				if existing.ID == "" && tc.ID != "" {
					existing.ID = tc.ID
				}
				if existing.Name == "" && tc.Function.Name != "" {
					existing.Name = tc.Function.Name
					_ = sw.Send("tool_call_start", sse.ToolCallStartData{
						ID:   existing.ID,
						Name: existing.Name,
					})
				}

				// accumulate arguments
				if tc.Function.Arguments != "" {
					existing.Arguments += tc.Function.Arguments
					_ = sw.Send("tool_call_args", sse.ToolCallArgsData{
						ID:               existing.ID,
						ArgumentsPartial: tc.Function.Arguments,
					})
				}
			}
		}
		stream.Close()

		if streamErr != nil {
			dl.Errorf("stream error: %v", streamErr)
			_ = sw.Send("error", sse.ErrorData{Code: "upstream_error", Message: streamErr.Error()})
			return streamErr
		}

		// build the assistant message
		assistantMsg := Message{
			Role: "assistant",
		}

		content := contentBuf.String()
		if content != "" {
			assistantMsg.Content = &content
		}

		// convert pending tool calls to finalized ToolCall slice
		if len(pending) > 0 {
			assistantMsg.ToolCalls = make([]ToolCall, 0, len(pending))
			for _, p := range pending {
				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, ToolCall{
					ID:   p.ID,
					Type: "function",
					Function: ToolCallFunction{
						Name:      p.Name,
						Arguments: p.Arguments,
					},
				})
			}
		}

		messages = append(messages, assistantMsg)

		// no tool calls — we're done
		if len(pending) == 0 {
			_ = sw.SendDone()
			return nil
		}

		// execute each tool call
		for _, p := range pending {
			result, durationMs := executeSingleTool(ctx, p, executor, sw, approvals)

			resultContent := result
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: p.ID,
				Content:    &resultContent,
			})

			_ = sw.Send("tool_call_result", sse.ToolCallResultData{
				ID:         p.ID,
				Name:       p.Name,
				Content:    result,
				DurationMS: durationMs,
			})
		}
	}

	// exhausted max iterations
	_ = sw.Send("error", sse.ErrorData{
		Code:    "max_iterations",
		Message: fmt.Sprintf("tool call loop exceeded %d iterations", maxToolLoopIterations),
	})
	return fmt.Errorf("tool call loop exceeded %d iterations", maxToolLoopIterations)
}

func executeSingleTool(
	ctx context.Context,
	p *pendingToolCall,
	executor ToolExecutor,
	sw *sse.Writer,
	approvals ApprovalRegistry,
) (result string, durationMs int64) {
	// approval gate
	if executor.NeedsApproval(p.Name) {
		_ = sw.Send("tool_call_approve", sse.ToolCallApproveData{
			ID:        p.ID,
			Name:      p.Name,
			Arguments: p.Arguments,
		})

		if approvals != nil {
			ch := approvals.Register(p.ID)
			defer approvals.Unregister(p.ID)

			select {
			case approved := <-ch:
				if !approved {
					return "Tool call denied by user", 0
				}
			case <-time.After(5 * time.Minute):
				return "Tool call approval timed out", 0
			case <-ctx.Done():
				return "Request cancelled", 0
			}
		}
	}

	_ = sw.Send("tool_call_executing", sse.ToolCallExecutingData{
		ID:   p.ID,
		Name: p.Name,
	})

	// parse arguments
	var args map[string]any
	if p.Arguments != "" {
		if err := json.Unmarshal([]byte(p.Arguments), &args); err != nil {
			dl.Warnf("malformed tool call arguments for %s: %v", p.Name, err)
			return fmt.Sprintf("Error: malformed arguments: %v", err), 0
		}
	}

	content, duration, err := executor.CallTool(ctx, p.Name, args)
	if err != nil {
		dl.Warnf("tool call %s failed: %v", p.Name, err)
		return fmt.Sprintf("Error: %v", err), duration.Milliseconds()
	}

	return content, duration.Milliseconds()
}
