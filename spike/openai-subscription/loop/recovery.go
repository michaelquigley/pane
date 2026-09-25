package loop

import (
	"errors"
	"fmt"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// RecordVersion versions the interrupted-turn record.
const RecordVersion = 1

// TurnState is the lifecycle of a submitted request.
type TurnState string

const (
	StateActive      TurnState = "active"
	StateComplete    TurnState = "complete"
	StateInterrupted TurnState = "interrupted"
)

// Reason explains an interruption.
type Reason string

const (
	ReasonCancelled  Reason = "cancelled"
	ReasonTransport  Reason = "transport"  // dropped or truncated stream
	ReasonIncomplete Reason = "incomplete" // terminal but unusable round
	ReasonProvider   Reason = "provider"   // auth, allowance, access, throttling
	ReasonProtocol   Reason = "protocol"
	ReasonBudget     Reason = "budget"
	ReasonRoundLimit Reason = "round_limit"
)

// CallState is a tool call's observed outcome.
type CallState string

const (
	CallPending       CallState = "pending"        // finalized, not yet approved/dispatched
	CallNotDispatched CallState = "not_dispatched" // established: never reached the executor
	CallDenied        CallState = "denied"         // established: not executed
	CallRejected      CallState = "rejected"       // established: malformed, not executed
	CallDispatched    CallState = "dispatched"     // handed to the executor; outcome pending
	CallCompleted     CallState = "completed"
	CallFailed        CallState = "failed"  // executor reported an error
	CallUnknown       CallState = "unknown" // dispatched; outcome not observed
)

// CallOutcome is one call's incremental record.
type CallOutcome struct {
	Call      round.ToolCall `json:"call"`
	State     CallState      `json:"state"`
	ErrorCode string         `json:"error_code,omitempty"`
	Result    *string        `json:"result,omitempty"`
	Note      string         `json:"note,omitempty"`

	// Reconciled is set when the user resolves an unknown outcome.
	Reconciled *Reconciliation `json:"reconciled,omitempty"`
}

// Reconciliation is the user's statement about an unknown outcome.
type Reconciliation struct {
	// Executed: "yes", "no", or "unknown" (the user cannot tell).
	Executed string `json:"executed"`
	Note     string `json:"note,omitempty"`
}

// RoundRecord tracks one committed assistant round's calls.
type RoundRecord struct {
	AssistantIndex   int           `json:"assistant_index"` // index in the turn's working history
	Finish           string        `json:"finish"`
	Calls            []CallOutcome `json:"calls,omitempty"`
	ResultsCommitted bool          `json:"results_committed"`
}

func (r *RoundRecord) hasState(s CallState) bool {
	for _, c := range r.Calls {
		if c.State == s {
			return true
		}
	}
	return false
}

// TurnRecord is the conversation's recovery record for one request. it is
// persisted beside the portable messages and never sent upstream.
type TurnRecord struct {
	Version    int           `json:"v"`
	State      TurnState     `json:"state"`
	Origin     round.Origin  `json:"origin"`
	HistoryLen int           `json:"history_len"` // messages before the turn
	Rounds     []RoundRecord `json:"rounds,omitempty"`

	Reason    Reason `json:"reason,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
	Message   string `json:"message,omitempty"`

	// PartialText is visibly interrupted output. it is display-only and is
	// not sent upstream as an assistant answer.
	PartialText *string `json:"partial_text,omitempty"`
	// PreviewCalls were streamed but never finalized; never executable.
	PreviewCalls []round.ToolCall `json:"preview_calls,omitempty"`
}

func (t *TurnRecord) markInterrupted(reason Reason, msg string, partial *string, previews []round.ToolCall) {
	t.State = StateInterrupted
	t.Reason = reason
	t.Message = msg
	t.PartialText = partial
	t.PreviewCalls = previews
	// anything finalized but not reached is established non-execution.
	for i := range t.Rounds {
		for j := range t.Rounds[i].Calls {
			if t.Rounds[i].Calls[j].State == CallPending {
				t.Rounds[i].Calls[j].State = CallNotDispatched
			}
			if t.Rounds[i].Calls[j].State == CallDispatched {
				// the dispatch mark persisted but no outcome did.
				t.Rounds[i].Calls[j].State = CallUnknown
			}
		}
	}
}

// Action is the recovery affordance an interrupted turn permits.
type Action string

const (
	ActionNone              Action = "none"                 // turn completed
	ActionRetryFromScratch  Action = "retry_from_scratch"   // non-execution established for every call
	ActionContinueAsNewTurn Action = "continue_as_new_turn" // recorded tool work exists
	ActionReconcile         Action = "reconcile"            // unresolved outcomes block tool-enabled continuation
)

// Assess returns the permitted recovery action. it never proposes
// re-dispatching a recorded call.
func Assess(t *TurnRecord) Action {
	if t.State != StateInterrupted {
		return ActionNone
	}
	work := false
	for _, r := range t.Rounds {
		for _, c := range r.Calls {
			switch c.State {
			case CallUnknown, CallDispatched:
				if c.Reconciled == nil {
					return ActionReconcile
				}
				work = true
			case CallCompleted, CallFailed:
				work = true
			}
		}
	}
	if work {
		return ActionContinueAsNewTurn
	}
	return ActionRetryFromScratch
}

// Reconcile records the user's resolution of an unknown outcome.
func Reconcile(t *TurnRecord, callID string, r Reconciliation) error {
	switch r.Executed {
	case "yes", "no", "unknown":
	default:
		return fmt.Errorf("invalid reconciliation '%s'", r.Executed)
	}
	for i := range t.Rounds {
		for j := range t.Rounds[i].Calls {
			c := &t.Rounds[i].Calls[j]
			if c.Call.ID == callID {
				if c.State != CallUnknown {
					return fmt.Errorf("call '%s' is not unresolved", callID)
				}
				c.Reconciled = &r
				return nil
			}
		}
	}
	return fmt.Errorf("call '%s' not found", callID)
}

// ErrNeedsReconciliation blocks continuation while outcomes are unknown.
var ErrNeedsReconciliation = errors.New("unresolved tool outcomes require reconciliation")

// PortableContinuation adapts an interrupted turn's committed messages into
// valid portable history for an explicit new user turn. every committed call
// gets an honest result message; nothing is presented as a success it was
// not. the caller appends the new user message.
func PortableContinuation(committed []round.Message, t *TurnRecord) ([]round.Message, error) {
	if Assess(t) == ActionReconcile {
		return nil, ErrNeedsReconciliation
	}
	out := make([]round.Message, 0, len(committed))
	answered := map[string]bool{}
	for _, m := range committed {
		if m.Role == "tool" {
			answered[m.ToolCallID] = true
		}
	}
	outcomes := map[string]CallOutcome{}
	for _, r := range t.Rounds {
		for _, c := range r.Calls {
			outcomes[c.Call.ID] = c
		}
	}
	for _, m := range committed {
		out = append(out, m)
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if answered[tc.ID] {
				continue
			}
			c, ok := outcomes[tc.ID]
			if !ok {
				return nil, fmt.Errorf("committed call '%s' has no recorded outcome", tc.ID)
			}
			out = append(out, round.Message{Role: "tool", ToolCallID: tc.ID, Content: round.Str(honestResult(c))})
		}
	}
	return out, nil
}

// honestResult states the recorded outcome in the tool-result slot.
func honestResult(c CallOutcome) string {
	switch c.State {
	case CallCompleted:
		return deref(c.Result)
	case CallFailed, CallDenied, CallRejected:
		return deref(c.Result)
	case CallNotDispatched:
		return "not executed: the turn was interrupted before this call was dispatched."
	case CallUnknown:
		switch c.Reconciled.Executed {
		case "yes":
			return "outcome not observed: the user reports this call did execute; its result is unavailable."
		case "no":
			return "not executed: the user reports this call did not execute."
		default:
			return "outcome unknown: this call may have executed; its result was not observed."
		}
	}
	return "outcome unknown."
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
