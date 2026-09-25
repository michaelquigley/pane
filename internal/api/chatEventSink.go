package api

import (
	"fmt"

	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/sse"
)

type chatEventSink struct {
	writer *sse.Writer
}

type roundCompleteData struct {
	Assistant    llm.Message
	ToolMessages []llm.Message
}

func (s chatEventSink) Emit(event llm.LoopEvent) error {
	call := event.Call
	switch event.Kind {
	case llm.LoopDelta:
		return s.writer.Send("delta", sse.DeltaData{Content: event.Content})
	case llm.LoopThinkingDelta:
		return s.writer.Send("thinking_delta", sse.ThinkingDeltaData{Content: event.Content})
	case llm.LoopToolCallStart:
		return s.writer.Send("tool_call_start", sse.ToolCallStartData{Index: call.Index, ID: call.ID, Name: call.Name})
	case llm.LoopToolCallArgs:
		return s.writer.Send("tool_call_args", sse.ToolCallArgsData{Index: call.Index, ID: call.ID, ArgumentsPartial: call.Arguments})
	case llm.LoopUsage:
		usage := event.Usage
		return s.writer.Send("usage", sse.UsageData{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens})
	case llm.LoopError:
		failure := event.Error
		return s.writer.Send("error", sse.ErrorData{Code: failure.Code, Message: failure.Message, ToolCallID: failure.ToolCallID})
	case llm.LoopToolCallApprove:
		return s.writer.Send("tool_call_approve", sse.ToolCallApproveData{Index: call.Index, ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	case llm.LoopToolCallExecuting:
		return s.writer.Send("tool_call_executing", sse.ToolCallExecutingData{Index: call.Index, ID: call.ID, Name: call.Name})
	case llm.LoopToolCallResult:
		result := event.Result
		return s.writer.Send("tool_call_result", sse.ToolCallResultData{
			Index: call.Index, ID: call.ID, Name: call.Name,
			Status: result.Status, ErrorCode: result.ErrorCode, Content: result.Content,
			DurationMS: result.DurationMS, ExecutionState: string(result.Dispatch),
		})
	case llm.LoopRoundComplete:
		return s.writer.Send("round_complete", roundCompleteData{Assistant: event.Round.Assistant, ToolMessages: event.Round.ToolMessages})
	case llm.LoopDone:
		return s.writer.SendDone()
	default:
		return fmt.Errorf("unknown loop event '%s'", event.Kind)
	}
}
