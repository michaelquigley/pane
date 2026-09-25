package llm

import (
	"context"
	"errors"
	"testing"
)

type cancellationSink struct {
	events []LoopEvent
	onEmit func(LoopEvent)
}

func (s *cancellationSink) Emit(event LoopEvent) error {
	s.events = append(s.events, event)
	if s.onEmit != nil {
		s.onEmit(event)
	}
	return nil
}

type waitingApproval struct {
	registered   int
	unregistered int
}

func (a *waitingApproval) Register(string) <-chan bool {
	a.registered++
	return make(chan bool)
}

func (a *waitingApproval) Unregister(string) {
	a.unregistered++
}

func TestCancellationStopsToolBatchWithoutSyntheticResults(t *testing.T) {
	for _, tt := range []struct {
		name          string
		cancelAt      LoopEventKind
		needsApproval bool
		wantCalls     int
		wantKinds     []LoopEventKind
	}{
		{
			name: "during approval", cancelAt: LoopToolCallApprove, needsApproval: true,
			wantKinds: []LoopEventKind{LoopDelta, LoopToolCallApprove},
		},
		{
			name: "between calls after received result", cancelAt: LoopToolCallResult, wantCalls: 1,
			wantKinds: []LoopEventKind{LoopDelta, LoopToolCallExecuting, LoopToolCallResult},
		},
		{
			name: "during executing event write", cancelAt: LoopToolCallExecuting,
			wantKinds: []LoopEventKind{LoopDelta, LoopToolCallExecuting},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sink := &cancellationSink{onEmit: func(event LoopEvent) {
				if event.Kind == tt.cancelAt {
					cancel()
				}
			}}
			adapter := &twoCallAdapter{}
			executor := &countingToolExecutor{approvalNeeded: tt.needsApproval}
			approvals := &waitingApproval{}
			err := RunToolLoop(ctx, adapter,
				[]Message{{Role: "user", Content: StringContent("read twice")}}, "test", 0,
				[]Tool{{Type: "function", Function: &FunctionDef{Name: "read"}}},
				executor, sink, approvals)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want cancellation", err)
			}
			if executor.calls != tt.wantCalls || adapter.rounds != 1 {
				t.Fatalf("calls = %d, rounds = %d; want %d call(s), one round", executor.calls, adapter.rounds, tt.wantCalls)
			}
			if len(sink.events) != len(tt.wantKinds) {
				t.Fatalf("event count = %d, want %d: %#v", len(sink.events), len(tt.wantKinds), sink.events)
			}
			for i, event := range sink.events {
				if event.Kind != tt.wantKinds[i] {
					t.Fatalf("event %d = %s, want %s", i, event.Kind, tt.wantKinds[i])
				}
			}
			if tt.wantCalls == 1 {
				result := sink.events[2]
				if result.Call.ID != "first" || result.Result == nil || result.Result.Dispatch != ResultReceived || result.Result.Content != "received" {
					t.Fatalf("received first result was not preserved: %#v", result)
				}
			}
			if tt.needsApproval && (approvals.registered != 1 || approvals.unregistered != 1) {
				t.Fatalf("approval registry was not cleaned up: %#v", approvals)
			}
		})
	}
}
