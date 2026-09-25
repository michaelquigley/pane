// Package loop is the harness's shared tool loop: it alone approves and
// executes tools, and it records tool outcomes incrementally so interrupted
// turns can be recovered honestly. it never re-dispatches a call to
// recover.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

// Executor runs a tool. the loop is its only caller.
type Executor interface {
	Call(ctx context.Context, name string, args map[string]any) (string, error)
}

// Approver decides whether a call may run.
type Approver interface {
	Approve(ctx context.Context, call round.ToolCall) (bool, error)
}

// ApproveAll approves every call.
type ApproveAll struct{}

func (ApproveAll) Approve(context.Context, round.ToolCall) (bool, error) { return true, nil }

// DenyAll denies every call.
type DenyAll struct{}

func (DenyAll) Approve(context.Context, round.ToolCall) (bool, error) { return false, nil }

// Recorder persists the turn record after every state change. in pane the
// equivalent mirror is not crash-safe; the harness recorder is a synced file.
type Recorder interface {
	Record(rec *TurnRecord) error
}

// Options bound a turn.
type Options struct {
	MaxRounds               int
	RepeatedFailureLimit    int
	CacheKey                string
	OnEvent                 func(round.Event)
	OnRoundComplete         func(f *round.Final, toolMessages []round.Message)
	AfterDispatchBeforeCall func(call round.ToolCall) // fault-injection hook for tests
}

// Result of a turn.
type Result struct {
	// Messages are the committed messages appended to the history.
	Messages []round.Message
	Record   *TurnRecord
	Rounds   int
}

// RunTurn runs one submitted request to completion or interruption. the
// adapter (model connection and settings) is fixed for the whole turn.
func RunTurn(ctx context.Context, a round.Adapter, history []round.Message, tools []round.Tool, exec Executor, approver Approver, rec Recorder, opt Options) (*Result, error) {
	if opt.MaxRounds <= 0 {
		opt.MaxRounds = 8
	}
	if opt.RepeatedFailureLimit <= 0 {
		opt.RepeatedFailureLimit = 2
	}
	offered := make(map[string]bool, len(tools))
	for _, t := range tools {
		offered[t.Name] = true
	}

	record := &TurnRecord{Version: RecordVersion, State: StateActive, Origin: round.Origin{Alias: a.Alias(), Identity: a.Identity()}, HistoryLen: len(history)}
	res := &Result{Record: record}
	save := func() error {
		if rec == nil {
			return nil
		}
		return rec.Record(record)
	}
	if err := save(); err != nil {
		return res, err
	}

	working := append([]round.Message(nil), history...)
	local := map[int]round.Local{}
	failures := map[string]int{}
	intent := round.IntentNormal

	for r := 0; r < opt.MaxRounds; r++ {
		res.Rounds++
		final, err := a.Round(ctx, round.Request{History: working, Tools: tools, Intent: intent, Local: local, CacheKey: opt.CacheKey}, opt.OnEvent)
		if err != nil {
			interrupt(record, err, ctx)
			return res, errors.Join(err, save())
		}
		if intent == round.IntentForcedFinal && len(final.Assistant.ToolCalls) > 0 {
			record.markInterrupted(ReasonProtocol, "model requested tools during forced-final recovery", final.Assistant.Content, final.Assistant.ToolCalls)
			return res, errors.Join(errors.New("tool calls during forced-final round"), save())
		}
		for _, tc := range final.Assistant.ToolCalls {
			if !offered[tc.Name] {
				record.markInterrupted(ReasonProtocol, fmt.Sprintf("model requested unoffered tool '%s'", tc.Name), final.Assistant.Content, final.Assistant.ToolCalls)
				return res, errors.Join(errors.New("unoffered tool requested"), save())
			}
		}

		// commit the finalized assistant round before any dispatch.
		idx := len(working)
		working = append(working, final.Assistant)
		res.Messages = append(res.Messages, final.Assistant)
		if final.Local != nil {
			local[idx] = *final.Local
		}
		record.Rounds = append(record.Rounds, RoundRecord{AssistantIndex: idx, Finish: string(final.Finish)})
		cur := &record.Rounds[len(record.Rounds)-1]
		for _, tc := range final.Assistant.ToolCalls {
			cur.Calls = append(cur.Calls, CallOutcome{Call: tc, State: CallPending})
		}
		if err := save(); err != nil {
			return res, err
		}

		if !final.Executable() {
			record.State = StateComplete
			if opt.OnRoundComplete != nil {
				opt.OnRoundComplete(final, nil)
			}
			return res, save()
		}

		var toolMessages []round.Message
		forceFinal := false
		for i := range cur.Calls {
			outcome := &cur.Calls[i]
			if ctx.Err() != nil {
				outcome.State = CallNotDispatched
				outcome.Note = "turn cancelled before dispatch"
				_ = save()
				continue
			}
			content, state, code := runCall(ctx, outcome.Call, exec, approver, func() error {
				outcome.State = CallDispatched
				if err := save(); err != nil {
					return err
				}
				if opt.AfterDispatchBeforeCall != nil {
					opt.AfterDispatchBeforeCall(outcome.Call)
				}
				return nil
			})
			outcome.State, outcome.ErrorCode = state, code
			if state == CallCompleted || state == CallFailed || state == CallDenied || state == CallRejected {
				outcome.Result = round.Str(content)
			}
			if err := save(); err != nil {
				return res, err
			}
			if state == CallFailed || state == CallDenied || state == CallRejected {
				key := outcome.Call.Name + "\x00" + outcome.Call.Arguments
				failures[key]++
				if failures[key] >= opt.RepeatedFailureLimit {
					forceFinal = true
				}
			}
		}

		if ctx.Err() != nil || cur.hasState(CallUnknown) || cur.hasState(CallNotDispatched) {
			record.markInterrupted(ReasonCancelled, "turn interrupted during tool execution", nil, nil)
			return res, errors.Join(context.Cause(ctx), save())
		}

		for _, c := range cur.Calls {
			tm := round.Message{Role: "tool", ToolCallID: c.Call.ID, Content: c.Result}
			working = append(working, tm)
			res.Messages = append(res.Messages, tm)
			toolMessages = append(toolMessages, tm)
		}
		cur.ResultsCommitted = true
		if err := save(); err != nil {
			return res, err
		}
		if opt.OnRoundComplete != nil {
			opt.OnRoundComplete(final, toolMessages)
		}
		if forceFinal {
			intent = round.IntentForcedFinal
		}
	}
	record.markInterrupted(ReasonRoundLimit, fmt.Sprintf("exceeded %d rounds", opt.MaxRounds), nil, nil)
	return res, errors.Join(errors.New("round limit reached"), save())
}

// runCall approves, parses, and executes one call. markDispatched is called
// immediately before the executor and must persist first.
func runCall(ctx context.Context, call round.ToolCall, exec Executor, approver Approver, markDispatched func() error) (string, CallState, string) {
	ok, err := approver.Approve(ctx, call)
	if err != nil {
		if ctx.Err() != nil {
			return "", CallNotDispatched, "cancelled"
		}
		return "tool call approval failed", CallDenied, "approval_error"
	}
	if !ok {
		return "tool call denied by user", CallDenied, "denied"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		return fmt.Sprintf("error: malformed arguments: %v", err), CallRejected, "malformed_arguments"
	}
	if ctx.Err() != nil {
		return "", CallNotDispatched, "cancelled"
	}
	if err := markDispatched(); err != nil {
		// the dispatch mark could not be persisted: do not run the tool.
		return "", CallNotDispatched, "record_failed"
	}
	out, err := exec.Call(ctx, call.Name, args)
	if err != nil {
		if ctx.Err() != nil {
			// the request may have reached the tool; its effect is unknown.
			return "", CallUnknown, "cancelled_during_execution"
		}
		return fmt.Sprintf("error: %v", err), CallFailed, "execution_error"
	}
	return out, CallCompleted, ""
}

func interrupt(record *TurnRecord, err error, ctx context.Context) {
	var re *round.Error
	reason := ReasonTransport
	msg := err.Error()
	var partial round.Partial
	if errors.As(err, &re) {
		partial = re.Partial
		switch re.Kind {
		case round.ErrCancelled:
			reason = ReasonCancelled
		case round.ErrTruncated, round.ErrTransport:
			reason = ReasonTransport
		case round.ErrIncomplete:
			reason = ReasonIncomplete
		case round.ErrAuth, round.ErrAllowance, round.ErrRateLimited, round.ErrModelAccess:
			reason = ReasonProvider
		case round.ErrBudget:
			reason = ReasonBudget
		default:
			reason = ReasonProtocol
		}
	} else if ctx.Err() != nil {
		reason = ReasonCancelled
	}
	var text *string
	if partial.Text != "" {
		text = round.Str(partial.Text)
	}
	record.markInterrupted(reason, msg, text, partial.ToolCalls)
	record.ErrorKind = string(round.KindOf(err))
}
