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

type roundCompleteData struct {
	Assistant    Message   `json:"assistant"`
	ToolMessages []Message `json:"tool_messages"`
}

type toolCallResult struct {
	Content    string
	DurationMS int64
	Status     string
	ErrorCode  string
}

const (
	toolCallStatusComplete = "complete"
	toolCallStatusError    = "error"

	toolCallErrorDenied             = "denied"
	toolCallErrorApprovalTimeout    = "approval_timeout"
	toolCallErrorCancelled          = "cancelled"
	toolCallErrorMalformedArguments = "malformed_arguments"
	toolCallErrorExecution          = "execution_error"
)

func emitToolCallStart(sw *sse.Writer, p *pendingToolCall) {
	_ = sw.Send("tool_call_start", sse.ToolCallStartData{
		Index: p.Index,
		ID:    p.ID,
		Name:  p.Name,
	})
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
		if ctx.Err() != nil {
			return ctx.Err()
		}

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
					emitToolCallStart(sw, existing)
				}

				// accumulate ID/name if they arrive in later chunks
				previousID := existing.ID
				previousName := existing.Name
				if tc.ID != "" {
					existing.ID = tc.ID
				}
				if tc.Function.Name != "" {
					existing.Name = tc.Function.Name
				}
				if existing.ID != previousID || existing.Name != previousName {
					emitToolCallStart(sw, existing)
				}

				// accumulate arguments
				if tc.Function.Arguments != "" {
					existing.Arguments += tc.Function.Arguments
					_ = sw.Send("tool_call_args", sse.ToolCallArgsData{
						Index:            existing.Index,
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

		toolMessages := make([]Message, 0, len(pending))

		// execute each tool call
		for _, p := range pending {
			result := executeSingleTool(ctx, p, executor, sw, approvals)

			resultContent := result.Content
			toolMsg := Message{
				Role:       "tool",
				ToolCallID: p.ID,
				Content:    &resultContent,
			}
			toolMessages = append(toolMessages, toolMsg)
			messages = append(messages, toolMsg)

			_ = sw.Send("tool_call_result", sse.ToolCallResultData{
				Index:      p.Index,
				ID:         p.ID,
				Name:       p.Name,
				Status:     result.Status,
				ErrorCode:  result.ErrorCode,
				Content:    result.Content,
				DurationMS: result.DurationMS,
			})
		}

		_ = sw.Send("round_complete", roundCompleteData{
			Assistant:    assistantMsg,
			ToolMessages: toolMessages,
		})

		// no tool calls — we're done
		if len(pending) == 0 {
			_ = sw.SendDone()
			return nil
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
) toolCallResult {
	// approval gate
	if executor.NeedsApproval(p.Name) {
		_ = sw.Send("tool_call_approve", sse.ToolCallApproveData{
			Index:     p.Index,
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
					return toolCallResult{
						Content:   "Tool call denied by user",
						Status:    toolCallStatusError,
						ErrorCode: toolCallErrorDenied,
					}
				}
			case <-time.After(5 * time.Minute):
				return toolCallResult{
					Content:   "Tool call approval timed out",
					Status:    toolCallStatusError,
					ErrorCode: toolCallErrorApprovalTimeout,
				}
			case <-ctx.Done():
				return toolCallResult{
					Content:   "Request cancelled",
					Status:    toolCallStatusError,
					ErrorCode: toolCallErrorCancelled,
				}
			}
		}
	}

	_ = sw.Send("tool_call_executing", sse.ToolCallExecutingData{
		Index: p.Index,
		ID:   p.ID,
		Name: p.Name,
	})

	// parse arguments
	var args map[string]any
	if p.Arguments != "" {
		if err := json.Unmarshal([]byte(p.Arguments), &args); err != nil {
			dl.Warnf("malformed tool call arguments for %s: %v", p.Name, err)
			return toolCallResult{
				Content:   fmt.Sprintf("Error: malformed arguments: %v", err),
				Status:    toolCallStatusError,
				ErrorCode: toolCallErrorMalformedArguments,
			}
		}
	}

	content, duration, err := executor.CallTool(ctx, p.Name, args)
	if err != nil {
		dl.Warnf("tool call %s failed: %v", p.Name, err)
		return toolCallResult{
			Content:    fmt.Sprintf("Error: %v", err),
			DurationMS: duration.Milliseconds(),
			Status:     toolCallStatusError,
			ErrorCode:  toolCallErrorExecution,
		}
	}

	return toolCallResult{
		Content:    content,
		DurationMS: duration.Milliseconds(),
		Status:     toolCallStatusComplete,
	}
}
