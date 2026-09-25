package llm

import (
	"context"
	"errors"
	"testing"
)

type twoCallAdapter struct{ rounds int }

func (a *twoCallAdapter) Round(ctx context.Context, _ RoundRequest, emit func(RoundEvent)) (RoundFinal, error) {
	a.rounds++
	emit(RoundEvent{Kind: "delta", Content: "working"})
	if err := ctx.Err(); err != nil {
		return RoundFinal{}, err
	}
	return RoundFinal{Finish: "tool_calls", Calls: []RoundCall{
		{Index: 0, ID: "first", Name: "read", Arguments: `{}`},
		{Index: 1, ID: "second", Name: "read", Arguments: `{}`},
	}}, nil
}

type countingToolExecutor struct {
	calls          int
	approvalNeeded bool
}

func (e *countingToolExecutor) NeedsApproval(string) bool { return e.approvalNeeded }

func (e *countingToolExecutor) CallTool(context.Context, string, map[string]any) ToolExecution {
	e.calls++
	return ToolExecution{Dispatch: ResultReceived, Content: "received"}
}

type failingLoopSink struct {
	failAt LoopEventKind
	err    error
	kinds  []LoopEventKind
}

func (s *failingLoopSink) Emit(event LoopEvent) error {
	s.kinds = append(s.kinds, event.Kind)
	if event.Kind == s.failAt {
		return s.err
	}
	return nil
}

type immediateApproval struct{}

func (immediateApproval) Register(string) <-chan bool {
	ch := make(chan bool, 1)
	ch <- true
	return ch
}

func (immediateApproval) Unregister(string) {}

func TestSinkFailureStopsFurtherToolDispatch(t *testing.T) {
	for _, tt := range []struct {
		name           string
		failAt         LoopEventKind
		approvalNeeded bool
		wantCalls      int
	}{
		{name: "stream delta", failAt: LoopDelta, wantCalls: 0},
		{name: "approval event", failAt: LoopToolCallApprove, approvalNeeded: true, wantCalls: 0},
		{name: "executing event", failAt: LoopToolCallExecuting, wantCalls: 0},
		{name: "between two calls", failAt: LoopToolCallResult, wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writeErr := errors.New("sink write failed")
			sink := &failingLoopSink{failAt: tt.failAt, err: writeErr}
			adapter := &twoCallAdapter{}
			executor := &countingToolExecutor{approvalNeeded: tt.approvalNeeded}
			err := RunToolLoop(context.Background(), adapter,
				[]Message{{Role: "user", Content: StringContent("read twice")}}, "test", 0,
				[]Tool{{Type: "function", Function: &FunctionDef{Name: "read"}}},
				executor, sink, immediateApproval{})
			if !errors.Is(err, writeErr) {
				t.Fatalf("error = %v, want sink failure", err)
			}
			if executor.calls != tt.wantCalls || adapter.rounds != 1 {
				t.Fatalf("calls = %d, rounds = %d; want %d call(s), one round", executor.calls, adapter.rounds, tt.wantCalls)
			}
			for _, kind := range sink.kinds {
				if kind == LoopRoundComplete || kind == LoopDone {
					t.Fatalf("emitted terminal event after sink failure: %v", sink.kinds)
				}
			}
		})
	}
}
