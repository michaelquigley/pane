package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
)

const (
	maxToolLoopIterations          = 20
	repeatedToolFailureThreshold   = 2
	forceFinalAfterToolFailureText = "tool calls are disabled because the same tool call failed repeatedly. provide a final answer without calling tools."
)

var toolCallIDSequence atomic.Uint64

// ToolExecutor abstracts MCP tool execution to avoid circular imports.
type ToolExecutor interface {
	CallTool(ctx context.Context, qualifiedName string, args map[string]any) ToolExecution
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

type toolCallResult struct {
	Content    string
	DurationMS int64
	Status     string
	ErrorCode  string
	Dispatch   Dispatch
}

type toolFailureTracker struct {
	counts map[string]int
}

const (
	toolCallStatusComplete = "complete"
	toolCallStatusError    = "error"

	toolCallErrorDenied             = "denied"
	toolCallErrorApprovalTimeout    = "approval_timeout"
	toolCallErrorMalformedArguments = "malformed_arguments"
	toolCallErrorExecution          = "execution_error"
)

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

	value, err := dd.DecodeStrictJSON([]byte(trimmed))
	if err != nil {
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
	client RoundAdapter,
	messages []Message,
	model string,
	maxTokens int,
	tools []Tool,
	executor ToolExecutor,
	sink LoopEventSink,
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

		request := RoundRequest{
			Model: model, Messages: requestMessages, MaxTokens: maxTokens,
			Iteration: iteration, Intent: IntentTools,
		}
		if forcedFinalRequest {
			request.Intent = IntentFinal
		} else if len(tools) > 0 {
			request.Tools = tools
		}

		roundCtx, cancelRound := context.WithCancel(ctx)
		var sinkErr error
		final, err := client.Round(roundCtx, request, func(event RoundEvent) {
			if sinkErr != nil {
				return
			}
			var output LoopEvent
			switch event.Kind {
			case "delta":
				output = LoopEvent{Kind: LoopDelta, Content: event.Content}
			case "thinking_delta":
				output = LoopEvent{Kind: LoopThinkingDelta, Content: event.Content}
			case "tool_call_start":
				output = LoopEvent{Kind: LoopToolCallStart, Call: event.Call}
			case "tool_call_args":
				output = LoopEvent{Kind: LoopToolCallArgs, Call: event.Call}
			case "usage":
				output = LoopEvent{Kind: LoopUsage, Usage: event.Usage}
			default:
				return
			}
			if sinkErr = sink.Emit(output); sinkErr != nil {
				cancelRound()
			}
		})
		cancelRound()
		if sinkErr != nil {
			return sinkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			var roundErr *RoundError
			code := "upstream_error"
			if errors.As(err, &roundErr) {
				code = roundErr.Kind
			}
			if sinkErr := emitLoopError(sink, code, err, ""); sinkErr != nil {
				return sinkErr
			}
			return err
		}
		if err := validateRoundFinal(final); err != nil {
			if sinkErr := emitLoopError(sink, "protocol", err, ""); sinkErr != nil {
				return sinkErr
			}
			return err
		}
		if forcedFinalRequest && len(final.Calls) > 0 {
			err := errors.New("model returned tool calls after repeated tool failures")
			if sinkErr := emitLoopError(sink, "repeated_tool_failure", err, ""); sinkErr != nil {
				return sinkErr
			}
			return err
		}
		pending := make([]*pendingToolCall, 0, len(final.Calls))
		for _, call := range final.Calls {
			pending = append(pending, &pendingToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments, Index: call.Index})
		}
		for _, p := range pending {
			if !offeredTool(tools, p.Name) {
				err := fmt.Errorf("model returned unoffered tool '%s'", p.Name)
				if sinkErr := emitLoopError(sink, "protocol", err, ""); sinkErr != nil {
					return sinkErr
				}
				return err
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
		content := final.Content
		if content == "" && len(pending) == 0 {
			message := "upstream returned an empty response: no content and no tool calls"
			err := errors.New(message)
			dl.Errorf("iteration %d: %v", iteration, err)
			if sinkErr := emitLoopError(sink, "empty_response", err, ""); sinkErr != nil {
				return sinkErr
			}
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

		if len(pending) > 0 {
			assistantMsg.ToolCalls = make([]ToolCall, 0, len(pending))
			for _, p := range pending {
				assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, ToolCall{ID: p.ID, Type: "function", Function: ToolCallFunction{Name: p.Name, Arguments: p.Arguments}})
			}
		}

		messages = append(messages, assistantMsg)

		toolMessages := make([]Message, 0, len(pending))

		// execute each tool call
		for _, p := range pending {
			result, err := executeSingleTool(ctx, p, executor, sink, approvals)
			if err != nil {
				return err
			}
			if result.Dispatch == NotDispatched {
				if err := ctx.Err(); err != nil {
					return err
				}
			}

			if result.Dispatch == UnknownDispatch {
				if err := emitToolResult(sink, p, result); err != nil {
					return err
				}
				err := fmt.Errorf("tool outcome unknown for '%s'", p.Name)
				if sinkErr := emitLoopError(sink, "tool_outcome_unknown", err, p.ID); sinkErr != nil {
					return sinkErr
				}
				return err
			}
			resultContent := result.Content
			toolMsg := Message{
				Role:       "tool",
				ToolCallID: p.ID,
				Content:    &resultContent,
			}
			toolMessages = append(toolMessages, toolMsg)
			messages = append(messages, toolMsg)

			if err := emitToolResult(sink, p, result); err != nil {
				return err
			}

			if failures.observe(p, result) {
				forceFinalResponse = true
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := sink.Emit(LoopEvent{Kind: LoopRoundComplete, Round: &LoopRound{
			Assistant: assistantMsg, ToolMessages: toolMessages,
		}}); err != nil {
			return err
		}

		// no tool calls — we're done
		if len(pending) == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			return sink.Emit(LoopEvent{Kind: LoopDone})
		}
	}

	// exhausted max iterations
	err := fmt.Errorf("tool call loop exceeded %d iterations", maxToolLoopIterations)
	if sinkErr := emitLoopError(sink, "max_iterations", err, ""); sinkErr != nil {
		return sinkErr
	}
	return err
}

func emitLoopError(sink LoopEventSink, code string, cause error, toolCallID string) error {
	return sink.Emit(LoopEvent{Kind: LoopError, Error: &LoopErrorData{
		Code: code, Message: cause.Error(), ToolCallID: toolCallID,
	}})
}

func emitToolResult(sink LoopEventSink, call *pendingToolCall, result toolCallResult) error {
	return sink.Emit(LoopEvent{Kind: LoopToolCallResult,
		Call: RoundCall{Index: call.Index, ID: call.ID, Name: call.Name},
		Result: &LoopToolResult{Status: result.Status, ErrorCode: result.ErrorCode,
			Content: result.Content, DurationMS: result.DurationMS, Dispatch: result.Dispatch},
	})
}

func validateRoundFinal(final RoundFinal) error {
	if final.Finish != "stop" && final.Finish != "tool_calls" {
		return roundProtocol("invalid normalized finish")
	}
	if final.Finish == "stop" && len(final.Calls) != 0 || final.Finish == "tool_calls" && len(final.Calls) == 0 {
		return roundProtocol("normalized finish does not match calls")
	}
	ids := make(map[string]bool, len(final.Calls))
	indices := make(map[int]bool, len(final.Calls))
	for _, call := range final.Calls {
		if call.Index < 0 || call.ID == "" || call.Name == "" || ids[call.ID] || indices[call.Index] || !objectJSON(call.Arguments) {
			return roundProtocol("invalid finalized tool call")
		}
		ids[call.ID] = true
		indices[call.Index] = true
	}
	return nil
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
	sink LoopEventSink,
	approvals ApprovalRegistry,
) (toolCallResult, error) {
	if ctx.Err() != nil {
		return toolCallResult{}, ctx.Err()
	}
	args, err := dd.DecodeStrictJSON([]byte(p.Arguments))
	if err != nil {
		return toolCallResult{Content: "error: malformed arguments", Status: toolCallStatusError, ErrorCode: toolCallErrorMalformedArguments, Dispatch: NotDispatched}, nil
	}
	// approval gate
	if executor.NeedsApproval(p.Name) {
		if approvals == nil {
			return toolCallResult{Content: "approval unavailable", Status: toolCallStatusError, ErrorCode: toolCallErrorDenied, Dispatch: NotDispatched}, nil
		}
		ch := approvals.Register(p.ID)
		defer approvals.Unregister(p.ID)
		if err := sink.Emit(LoopEvent{Kind: LoopToolCallApprove, Call: RoundCall{
			Index: p.Index, ID: p.ID, Name: p.Name, Arguments: p.Arguments,
		}}); err != nil {
			return toolCallResult{}, err
		}

		{
			select {
			case approved := <-ch:
				if !approved {
					return toolCallResult{
						Content:   "tool call denied by user",
						Status:    toolCallStatusError,
						ErrorCode: toolCallErrorDenied,
						Dispatch:  NotDispatched,
					}, nil
				}
			case <-time.After(5 * time.Minute):
				return toolCallResult{
					Content:   "tool call approval timed out",
					Status:    toolCallStatusError,
					ErrorCode: toolCallErrorApprovalTimeout,
					Dispatch:  NotDispatched,
				}, nil
			case <-ctx.Done():
				return toolCallResult{}, ctx.Err()
			}
		}
	}
	if ctx.Err() != nil {
		return toolCallResult{}, ctx.Err()
	}

	if err := sink.Emit(LoopEvent{Kind: LoopToolCallExecuting, Call: RoundCall{
		Index: p.Index, ID: p.ID, Name: p.Name,
	}}); err != nil {
		return toolCallResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return toolCallResult{}, err
	}

	execution := executor.CallTool(ctx, p.Name, args)
	if execution.Dispatch == NotDispatched && ctx.Err() != nil {
		return toolCallResult{}, ctx.Err()
	}
	if execution.Dispatch != NotDispatched && execution.Dispatch != ResultReceived && execution.Dispatch != UnknownDispatch {
		execution.Dispatch = UnknownDispatch
		execution.Err = errors.New("executor omitted dispatch evidence")
	}
	if execution.Dispatch == UnknownDispatch {
		diagnostic := "execution outcome unknown"
		if execution.Err != nil {
			diagnostic = execution.Err.Error()
		}
		return toolCallResult{
			Content:    "error: " + diagnostic,
			DurationMS: execution.Duration.Milliseconds(),
			Status:     toolCallStatusError,
			ErrorCode:  toolCallErrorExecution,
			Dispatch:   UnknownDispatch,
		}, nil
	}
	if execution.Err != nil || execution.IsError || execution.Dispatch == NotDispatched {
		content := execution.Content
		if content == "" && execution.Err != nil {
			content = fmt.Sprintf("error: %v", execution.Err)
		}
		return toolCallResult{Content: content, DurationMS: execution.Duration.Milliseconds(), Status: toolCallStatusError, ErrorCode: toolCallErrorExecution, Dispatch: execution.Dispatch}, nil
	}

	return toolCallResult{
		Content:    execution.Content,
		DurationMS: execution.Duration.Milliseconds(),
		Status:     toolCallStatusComplete,
		Dispatch:   ResultReceived,
	}, nil
}

func offeredTool(tools []Tool, name string) bool {
	for _, tool := range tools {
		if tool.Function != nil && tool.Function.Name == name {
			return true
		}
	}
	return false
}
