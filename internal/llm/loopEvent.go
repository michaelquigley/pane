package llm

type LoopEventKind string

const (
	LoopDelta             LoopEventKind = "delta"
	LoopThinkingDelta     LoopEventKind = "thinking_delta"
	LoopToolCallStart     LoopEventKind = "tool_call_start"
	LoopToolCallArgs      LoopEventKind = "tool_call_args"
	LoopUsage             LoopEventKind = "usage"
	LoopError             LoopEventKind = "error"
	LoopToolCallApprove   LoopEventKind = "tool_call_approve"
	LoopToolCallExecuting LoopEventKind = "tool_call_executing"
	LoopToolCallResult    LoopEventKind = "tool_call_result"
	LoopRoundComplete     LoopEventKind = "round_complete"
	LoopDone              LoopEventKind = "done"
)

type LoopEvent struct {
	Kind    LoopEventKind
	Content string
	Call    RoundCall
	Usage   *Usage
	Error   *LoopErrorData
	Result  *LoopToolResult
	Round   *LoopRound
}

type LoopErrorData struct {
	Code       string
	Message    string
	ToolCallID string
}

type LoopToolResult struct {
	Status     string
	ErrorCode  string
	Content    string
	DurationMS int64
	Dispatch   Dispatch
}

type LoopRound struct {
	Assistant    Message
	ToolMessages []Message
}

type LoopEventSink interface {
	Emit(LoopEvent) error
}
