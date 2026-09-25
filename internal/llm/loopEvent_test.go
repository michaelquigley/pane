package llm

import (
	"context"
	"fmt"

	"github.com/michaelquigley/pane/internal/sse"
)

type testSSELoopSink struct {
	writer *sse.Writer
}

type roundCompleteData struct {
	Assistant    Message
	ToolMessages []Message
}

func (s testSSELoopSink) Emit(event LoopEvent) error {
	call := event.Call
	switch event.Kind {
	case LoopDelta:
		return s.writer.Send("delta", sse.DeltaData{Content: event.Content})
	case LoopThinkingDelta:
		return s.writer.Send("thinking_delta", sse.ThinkingDeltaData{Content: event.Content})
	case LoopToolCallStart:
		return s.writer.Send("tool_call_start", sse.ToolCallStartData{Index: call.Index, ID: call.ID, Name: call.Name})
	case LoopToolCallArgs:
		return s.writer.Send("tool_call_args", sse.ToolCallArgsData{Index: call.Index, ID: call.ID, ArgumentsPartial: call.Arguments})
	case LoopUsage:
		return s.writer.Send("usage", sse.UsageData{PromptTokens: event.Usage.PromptTokens, CompletionTokens: event.Usage.CompletionTokens, TotalTokens: event.Usage.TotalTokens})
	case LoopError:
		return s.writer.Send("error", sse.ErrorData{Code: event.Error.Code, Message: event.Error.Message, ToolCallID: event.Error.ToolCallID})
	case LoopToolCallApprove:
		return s.writer.Send("tool_call_approve", sse.ToolCallApproveData{Index: call.Index, ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	case LoopToolCallExecuting:
		return s.writer.Send("tool_call_executing", sse.ToolCallExecutingData{Index: call.Index, ID: call.ID, Name: call.Name})
	case LoopToolCallResult:
		result := event.Result
		return s.writer.Send("tool_call_result", sse.ToolCallResultData{
			Index: call.Index, ID: call.ID, Name: call.Name,
			Status: result.Status, ErrorCode: result.ErrorCode, Content: result.Content,
			DurationMS: result.DurationMS, ExecutionState: string(result.Dispatch),
		})
	case LoopRoundComplete:
		return s.writer.Send("round_complete", roundCompleteData{Assistant: event.Round.Assistant, ToolMessages: event.Round.ToolMessages})
	case LoopDone:
		return s.writer.SendDone()
	default:
		return fmt.Errorf("unknown test loop event '%s'", event.Kind)
	}
}

func runTestToolLoop(ctx context.Context, client RoundAdapter, messages []Message, model string,
	maxTokens int, tools []Tool, executor ToolExecutor, writer *sse.Writer, approvals ApprovalRegistry,
) error {
	return RunToolLoop(ctx, client, messages, model, maxTokens, tools, executor, testSSELoopSink{writer: writer}, approvals)
}
