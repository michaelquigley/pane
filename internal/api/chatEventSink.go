package api

import (
	"fmt"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/sse"
)

// chatEventSink maps the shared loop's lifecycle onto pane's SSE protocol.
// every event names the turn; critical lifecycle events also carry a
// per-turn sequence number so the browser can detect loss and duplication.
type chatEventSink struct {
	writer *sse.Writer
	turnID string
	seq    int
	// failure records the last loop error, for availability reporting.
	failure *llm.LoopErrorData
	end     *llm.TurnEnd
}

type roundReadyData struct {
	TurnID    string
	Seq       int
	RoundID   string
	Assistant llm.Message
	Finish    string
}

type roundCompleteData struct {
	TurnID       string `dd:",+omitempty"`
	Seq          int    `dd:",+omitempty"`
	RoundID      string `dd:",+omitempty"`
	Assistant    llm.Message
	ToolMessages []llm.Message
}

func (s *chatEventSink) next() int {
	s.seq++
	return s.seq
}

func (s *chatEventSink) Emit(event llm.LoopEvent) error {
	call := event.Call
	turn, round := s.turnID, event.RoundID
	switch event.Kind {
	case llm.LoopTurnStart:
		data := sse.TurnStartData{TurnID: turn, Seq: s.next(), Alias: event.Turn.Alias}
		if event.Turn.Origin != nil {
			origin, err := dd.Unbind(event.Turn.Origin)
			if err != nil {
				return err
			}
			data.Origin = origin
		}
		return s.writer.Send("turn_start", data)
	case llm.LoopRoundReady:
		return s.writer.Send("round_ready", roundReadyData{TurnID: turn, Seq: s.next(), RoundID: round, Assistant: event.Round.Assistant, Finish: event.Round.Finish})
	case llm.LoopTurnEnd:
		end := event.End
		s.end = end
		return s.writer.Send("turn_end", sse.TurnEndData{TurnID: turn, Seq: s.next(), Outcome: end.Outcome, Execution: end.Execution,
			ErrorCode: end.ErrorCode, Message: end.Message, PartialText: end.PartialText})
	case llm.LoopDelta:
		return s.writer.Send("delta", sse.DeltaData{TurnID: turn, RoundID: round, Content: event.Content})
	case llm.LoopThinkingDelta:
		return s.writer.Send("thinking_delta", sse.ThinkingDeltaData{TurnID: turn, RoundID: round, Content: event.Content})
	case llm.LoopToolCallStart:
		return s.writer.Send("tool_call_start", sse.ToolCallStartData{TurnID: turn, RoundID: round, Index: call.Index, ID: call.ID, Name: call.Name})
	case llm.LoopToolCallArgs:
		return s.writer.Send("tool_call_args", sse.ToolCallArgsData{TurnID: turn, RoundID: round, Index: call.Index, ID: call.ID, ArgumentsPartial: call.Arguments})
	case llm.LoopUsage:
		usage := event.Usage
		return s.writer.Send("usage", sse.UsageData{TurnID: turn, RoundID: round, PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens})
	case llm.LoopError:
		failure := event.Error
		s.failure = failure
		return s.writer.Send("error", sse.ErrorData{TurnID: turn, Code: failure.Code, Message: failure.Message, ToolCallID: failure.ToolCallID})
	case llm.LoopToolCallApprove:
		return s.writer.Send("tool_call_approve", sse.ToolCallApproveData{TurnID: turn, Seq: s.next(), RoundID: round, Index: call.Index, ID: call.ID, Name: call.Name, Arguments: call.Arguments})
	case llm.LoopToolCallExecuting:
		return s.writer.Send("tool_call_executing", sse.ToolCallExecutingData{TurnID: turn, Seq: s.next(), RoundID: round, Index: call.Index, ID: call.ID, Name: call.Name})
	case llm.LoopToolCallResult:
		result := event.Result
		return s.writer.Send("tool_call_result", sse.ToolCallResultData{
			TurnID: turn, Seq: s.next(), RoundID: round,
			Index: call.Index, ID: call.ID, Name: call.Name,
			Status: result.Status, ErrorCode: result.ErrorCode, Content: result.Content,
			DurationMS: result.DurationMS, ExecutionState: string(result.Dispatch),
		})
	case llm.LoopRoundComplete:
		return s.writer.Send("round_complete", roundCompleteData{TurnID: turn, Seq: s.next(), RoundID: round, Assistant: event.Round.Assistant, ToolMessages: event.Round.ToolMessages})
	case llm.LoopDone:
		return s.writer.SendDone()
	default:
		return fmt.Errorf("unknown loop event '%s'", event.Kind)
	}
}
