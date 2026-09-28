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

	// lifecycle events carry the authoritative turn record; the visual
	// deltas above stay separate from it.
	LoopTurnStart  LoopEventKind = "turn_start"
	LoopRoundReady LoopEventKind = "round_ready"
	LoopTurnEnd    LoopEventKind = "turn_end"
)

type LoopEvent struct {
	Kind    LoopEventKind
	RoundID string
	Content string
	Call    RoundCall
	Usage   *Usage
	Error   *LoopErrorData
	Result  *LoopToolResult
	Round   *LoopRound
	Turn    *Turn
	End     *TurnEnd
}

// Turn identifies one submitted request: its id, the selected alias, and the
// resolved connection origin fixed for every round of the turn.
type Turn struct {
	ID     string
	Alias  string
	Origin *RoundOrigin
}

const (
	TurnCompleted = "completed"
	TurnFailed    = "failed"
	TurnCancelled = "cancelled"

	ExecutionNone    = "none"
	ExecutionKnown   = "known"
	ExecutionUnknown = "unknown"
)

// TurnEnd is the authoritative terminal record of a turn.
type TurnEnd struct {
	Outcome     string
	Execution   string
	ErrorCode   string
	Message     string
	PartialText string
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
	Finish       string
}

type LoopEventSink interface {
	Emit(LoopEvent) error
}
