package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/sse"
)

const (
	maxToolLoopIterations          = 20
	repeatedToolFailureThreshold   = 2
	forceFinalAfterToolFailureText = "tool calls are disabled because the same tool call failed repeatedly. provide a final answer without calling tools."
)

var toolCallIDSequence atomic.Uint64

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

type toolFailureTracker struct {
	counts map[string]int
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

func nextToolCallID(iteration, index int) string {
	seq := toolCallIDSequence.Add(1)
	return fmt.Sprintf("pane_call_%d_%d_%d", seq, iteration, index)
}

func newToolFailureTracker() *toolFailureTracker {
	return &toolFailureTracker{
		counts: make(map[string]int),
	}
}

func (t *toolFailureTracker) observe(p *pendingToolCall, result toolCallResult) bool {
	key := toolFailureKey(p)
	if result.Status == toolCallStatusComplete {
		delete(t.counts, key)
		return false
	}
	if !isRepeatableToolFailure(result.ErrorCode) {
		return false
	}

	t.counts[key]++
	return t.counts[key] >= repeatedToolFailureThreshold
}

func isRepeatableToolFailure(errorCode string) bool {
	switch errorCode {
	case toolCallErrorDenied,
		toolCallErrorApprovalTimeout,
		toolCallErrorMalformedArguments,
		toolCallErrorExecution:
		return true
	default:
		return false
	}
}

func toolFailureKey(p *pendingToolCall) string {
	return p.Name + "\x00" + normalizeToolArguments(p.Arguments)
}

func normalizeToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return ""
	}

	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return trimmed
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return trimmed
	}
	return string(normalized)
}

// RunToolLoop runs the full chat-with-tools loop: stream LLM response, execute
// tool calls via MCP, append results, re-send until the LLM produces a final
// content-only response.
func RunToolLoop(
	ctx context.Context,
	client *Client,
	messages []Message,
	model string,
	maxTokens int,
	tools []Tool,
	executor ToolExecutor,
	sw *sse.Writer,
	approvals ApprovalRegistry,
) error {
	failures := newToolFailureTracker()
	forceFinalResponse := false

	// the history arrives from the browser (and, after an earlier round, from
	// this loop). drop any assistant message that says nothing -- carries no
	// content and no tool calls: strict providers reject it on the wire, and
	// an interrupted or empty-completing turn can leave one in stored history.
	// dropping it is lossless and unsticks the conversation.
	messages = dropEmptyAssistants(messages)

	for iteration := 0; iteration < maxToolLoopIterations || forceFinalResponse; iteration++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		forcedFinalRequest := forceFinalResponse
		forceFinalResponse = false

		requestMessages := messages
		if forcedFinalRequest {
			requestMessages = append([]Message(nil), messages...)
			requestMessages = append(requestMessages, Message{
				Role:    "system",
				Content: StringContent(forceFinalAfterToolFailureText),
			})
		}

		req := &ChatRequest{
			Model:     model,
			Messages:  requestMessages,
			MaxTokens: maxTokens,
		}
		if len(tools) > 0 && !forcedFinalRequest {
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
		sawToolCallDelta := false
		sawThinkingDelta := false
		var finishReason string

		for {
			chunk, err := stream.Recv()
			if err != nil {
				if err == io.EOF {
					break
				}
				streamErr = err
				break
			}

			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta

				// the last non-empty finish reason the stream reported: it is
				// what separates a truncated completion from a clean one
				if chunk.Choices[0].FinishReason != nil && *chunk.Choices[0].FinishReason != "" {
					finishReason = *chunk.Choices[0].FinishReason
				}

				// content tokens
				if delta.Content != nil && *delta.Content != "" {
					contentBuf.WriteString(*delta.Content)
					_ = sw.Send("delta", sse.DeltaData{Content: *delta.Content})
				}

				// thinking tokens: pass-through only. llm.Message carries no
				// reasoning field, so the re-sent history never echoes them.
				if delta.Reasoning != nil && *delta.Reasoning != "" {
					sawThinkingDelta = true
					_ = sw.Send("thinking_delta", sse.ThinkingDeltaData{Content: *delta.Reasoning})
				}

				// tool call tokens
				if len(delta.ToolCalls) > 0 {
					sawToolCallDelta = true
				}
				for _, tc := range delta.ToolCalls {
					idx := 0
					if tc.Index != nil {
						idx = *tc.Index
					}

					existing, ok := pending[idx]
					if !ok {
						existing = &pendingToolCall{
							ID:    nextToolCallID(iteration, idx),
							Name:  tc.Function.Name,
							Index: idx,
						}
						pending[idx] = existing
						emitToolCallStart(sw, existing)
					}

					// accumulate name if it arrives in later chunks
					if tc.Function.Name != "" {
						existing.Name = tc.Function.Name
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

			if chunk.Usage != nil {
				_ = sw.Send("usage", sse.UsageData{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				})
			}
		}
		stream.Close()

		if streamErr != nil {
			dl.Errorf("stream error: %v", streamErr)
			_ = sw.Send("error", sse.ErrorData{Code: "upstream_error", Message: streamErr.Error()})
			return streamErr
		}

		if forcedFinalRequest && sawToolCallDelta {
			err := fmt.Errorf("model returned tool calls after repeated tool failures")
			_ = sw.Send("error", sse.ErrorData{Code: "repeated_tool_failure", Message: err.Error()})
			return err
		}

		// discard incomplete tool calls (empty name from partial LLM deltas)
		for idx, p := range pending {
			if p.Name == "" {
				dl.Debugf("discarding incomplete tool call at index %d (id=%q, args=%q)", idx, p.ID, p.Arguments)
				delete(pending, idx)
			}
		}

		// a round that produced neither content nor tool calls is a failure,
		// not a success: committing the empty assistant message would poison
		// the history -- strict providers reject it on the next request -- so
		// report the empty completion and commit nothing. the finish reason
		// separates the two causes: 'length' means the model spent its whole
		// output budget (thinking, for reasoning models) before producing
		// anything, which is a backend budget problem the operator can fix;
		// anything else is a genuinely empty response.
		content := contentBuf.String()
		if content == "" && len(pending) == 0 {
			message := "upstream returned an empty response: no content and no tool calls"
			if finishReason == "length" {
				message = "the model reached its output token limit before producing any content or tool calls"
				if sawThinkingDelta {
					message += " -- it spent the whole budget thinking. raise the backend's output token cap (or the model's thinking budget) and retry"
				}
			}
			err := errors.New(message)
			dl.Errorf("iteration %d: %v", iteration, err)
			_ = sw.Send("error", sse.ErrorData{Code: "empty_response", Message: err.Error()})
			return err
		}

		// build the assistant message
		assistantMsg := Message{
			Role: "assistant",
		}

		if content != "" {
			assistantMsg.Content = &content
		}
		dl.Debugf("iteration %d: content=%q, pending=%d", iteration, content, len(pending))

		// collect pending tool calls in stable index order
		sortedPending := make([]*pendingToolCall, 0, len(pending))
		for _, p := range pending {
			sortedPending = append(sortedPending, p)
		}
		slices.SortFunc(sortedPending, func(a, b *pendingToolCall) int {
			return a.Index - b.Index
		})

		// convert pending tool calls to finalized ToolCall slice
		if len(sortedPending) > 0 {
			assistantMsg.ToolCalls = make([]ToolCall, 0, len(sortedPending))
			for _, p := range sortedPending {
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

		toolMessages := make([]Message, 0, len(sortedPending))

		// execute each tool call
		for _, p := range sortedPending {
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

			if failures.observe(p, result) {
				forceFinalResponse = true
			}
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

// dropEmptyAssistants removes assistant messages that carry neither content
// nor tool calls. it runs once, over the browser-supplied history, before the
// first upstream request: the loop itself only ever appends assistant
// messages that passed the empty-completion check, so a second pass would be
// dead code.
func dropEmptyAssistants(messages []Message) []Message {
	kept := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == "assistant" && len(message.ToolCalls) == 0 &&
			(message.Content == nil || *message.Content == "") {
			dl.Debugf("dropping empty assistant message from request history")
			continue
		}
		kept = append(kept, message)
	}
	return kept
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
						Content:   "tool call denied by user",
						Status:    toolCallStatusError,
						ErrorCode: toolCallErrorDenied,
					}
				}
			case <-time.After(5 * time.Minute):
				return toolCallResult{
					Content:   "tool call approval timed out",
					Status:    toolCallStatusError,
					ErrorCode: toolCallErrorApprovalTimeout,
				}
			case <-ctx.Done():
				return toolCallResult{
					Content:   "request cancelled",
					Status:    toolCallStatusError,
					ErrorCode: toolCallErrorCancelled,
				}
			}
		}
	}

	_ = sw.Send("tool_call_executing", sse.ToolCallExecutingData{
		Index: p.Index,
		ID:    p.ID,
		Name:  p.Name,
	})

	// parse arguments
	var args map[string]any
	if p.Arguments != "" {
		if err := json.Unmarshal([]byte(p.Arguments), &args); err != nil {
			dl.Warnf("malformed tool call arguments for %s: %v", p.Name, err)
			return toolCallResult{
				Content:   fmt.Sprintf("error: malformed arguments: %v", err),
				Status:    toolCallStatusError,
				ErrorCode: toolCallErrorMalformedArguments,
			}
		}
	}

	content, duration, err := executor.CallTool(ctx, p.Name, args)
	if err != nil {
		dl.Warnf("tool call %s failed: %v", p.Name, err)
		return toolCallResult{
			Content:    fmt.Sprintf("error: %v", err),
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
