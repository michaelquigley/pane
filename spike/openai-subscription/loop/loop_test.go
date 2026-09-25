package loop_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/michaelquigley/pane/spike/openai-subscription/convo"
	"github.com/michaelquigley/pane/spike/openai-subscription/loop"
	"github.com/michaelquigley/pane/spike/openai-subscription/mcpfix"
	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

var fixtureBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pane-spike-fixture-")
	if err != nil {
		panic(err)
	}
	fixtureBin = filepath.Join(dir, "mcpadd")
	out, err := exec.Command("go", "build", "-o", fixtureBin, "../mcpfix/mcpadd").CombinedOutput()
	if err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type fixture struct {
	client *mcpfix.Client
	log    string
}

func startFixture(t *testing.T, delay string) *fixture {
	t.Helper()
	log := filepath.Join(t.TempDir(), "exec.log")
	env := []string{"PANE_SPIKE_EXEC_LOG=" + log}
	if delay != "" {
		env = append(env, "PANE_SPIKE_ADD_DELAY="+delay)
	}
	c, err := mcpfix.Start(context.Background(), fixtureBin, env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &fixture{client: c, log: log}
}

func (f *fixture) executions() int {
	b, _ := os.ReadFile(f.log)
	return strings.Count(string(b), "\n")
}

// scripted is a mock adapter returning canned rounds.
type scripted struct {
	steps    []func(req round.Request) (*round.Final, error)
	requests []round.Request
}

func (s *scripted) Identity() round.Identity {
	return round.Identity{Provider: "mock", Protocol: "mock", UpstreamModel: "m", Service: "mock"}
}
func (s *scripted) Alias() string { return "mock" }
func (s *scripted) Round(ctx context.Context, req round.Request, emit func(round.Event)) (*round.Final, error) {
	s.requests = append(s.requests, req)
	n := len(s.requests) - 1
	if n >= len(s.steps) {
		return nil, errors.New("unexpected round")
	}
	return s.steps[n](req)
}

func toolRound(calls ...round.ToolCall) func(round.Request) (*round.Final, error) {
	return func(round.Request) (*round.Final, error) {
		return &round.Final{Finish: round.FinishToolCalls, Assistant: round.Message{Role: "assistant", ToolCalls: calls}}, nil
	}
}

func answer(text string) func(round.Request) (*round.Final, error) {
	return func(round.Request) (*round.Final, error) {
		return &round.Final{Finish: round.FinishStop, Assistant: round.Message{Role: "assistant", Content: round.Str(text)}}, nil
	}
}

func fail(kind round.ErrorKind, partial round.Partial) func(round.Request) (*round.Final, error) {
	return func(round.Request) (*round.Final, error) {
		return nil, &round.Error{Kind: kind, Message: string(kind), Partial: partial}
	}
}

var history = []round.Message{{Role: "system", Content: round.Str("sys")}, {Role: "user", Content: round.Str("add 2 and 2")}}

func add(id string, a, b string) round.ToolCall {
	return round.ToolCall{ID: id, Name: "add", Arguments: `{"a":` + a + `,"b":` + b + `}`}
}

func TestToolLoopExecutesOnce(t *testing.T) {
	fx := startFixture(t, "")
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(add("c1", "2", "2")), answer("4")}}
	res, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, nil, loop.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fx.executions() != 1 || len(res.Messages) != 3 || *res.Messages[1].Content != "4" || res.Record.State != loop.StateComplete {
		t.Fatalf("executions %d messages %+v", fx.executions(), res.Messages)
	}
	second := ad.requests[1].History
	if last := second[len(second)-1]; last.Role != "tool" || last.ToolCallID != "c1" {
		t.Fatalf("result not sent back: %+v", last)
	}
}

func TestDeniedCallsNeverExecute(t *testing.T) {
	fx := startFixture(t, "")
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(add("c1", "2", "2")), answer("ok")}}
	res, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.DenyAll{}, nil, loop.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fx.executions() != 0 {
		t.Fatal("denied call executed")
	}
	if c := res.Record.Rounds[0].Calls[0]; c.State != loop.CallDenied || *c.Result != "tool call denied by user" {
		t.Fatalf("outcome %+v", c)
	}
}

func TestIncompleteRoundsNeverDispatch(t *testing.T) {
	for _, kind := range []round.ErrorKind{round.ErrTruncated, round.ErrIncomplete, round.ErrCancelled, round.ErrTransport, round.ErrProtocol} {
		fx := startFixture(t, "")
		partial := round.Partial{Text: "let me", ToolCalls: []round.ToolCall{{Name: "add", Arguments: `{"a":2,`}}}
		ad := &scripted{steps: []func(round.Request) (*round.Final, error){fail(kind, partial)}}
		res, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, nil, loop.Options{})
		if err == nil || fx.executions() != 0 {
			t.Fatalf("%s: err=%v executions=%d", kind, err, fx.executions())
		}
		if res.Record.State != loop.StateInterrupted || *res.Record.PartialText != "let me" || len(res.Record.PreviewCalls) != 1 {
			t.Fatalf("%s: record %+v", kind, res.Record)
		}
		if loop.Assess(res.Record) != loop.ActionRetryFromScratch {
			t.Fatalf("%s: %s", kind, loop.Assess(res.Record))
		}
		if len(res.Messages) != 0 {
			t.Fatalf("%s: incomplete round committed", kind)
		}
	}
}

func TestObservedResultThenDisconnect(t *testing.T) {
	fx := startFixture(t, "")
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(add("c1", "2", "2")), fail(round.ErrTransport, round.Partial{Text: "the ans"})}}
	res, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, nil, loop.Options{})
	if round.KindOf(err) != round.ErrTransport {
		t.Fatalf("got %v", err)
	}
	if fx.executions() != 1 || len(ad.requests) != 2 {
		t.Fatalf("executions %d rounds %d: the harness must not re-run to recover", fx.executions(), len(ad.requests))
	}
	if loop.Assess(res.Record) != loop.ActionContinueAsNewTurn {
		t.Fatalf("assess %s", loop.Assess(res.Record))
	}
	cont, err := loop.PortableContinuation(res.Messages, res.Record)
	if err != nil || len(cont) != 2 || *cont[1].Content != "4" {
		t.Fatalf("continuation %+v %v", cont, err)
	}
	time.Sleep(50 * time.Millisecond)
	if fx.executions() != 1 {
		t.Fatal("late execution")
	}
}

func TestUnknownOutcomeRequiresReconciliation(t *testing.T) {
	fx := startFixture(t, "3s")
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(add("c1", "2", "2"))}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opt := loop.Options{AfterDispatchBeforeCall: func(round.ToolCall) {
		go func() {
			for fx.executions() == 0 {
				time.Sleep(5 * time.Millisecond)
			}
			cancel() // the tool has acted; its result will never be observed
		}()
	}}
	res, err := loop.RunTurn(ctx, ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, nil, opt)
	if err == nil {
		t.Fatal("expected interruption")
	}
	c := res.Record.Rounds[0].Calls[0]
	if c.State != loop.CallUnknown || c.Result != nil {
		t.Fatalf("outcome %+v", c)
	}
	if loop.Assess(res.Record) != loop.ActionReconcile {
		t.Fatal("unknown outcome did not block continuation")
	}
	if _, err := loop.PortableContinuation(res.Messages, res.Record); !errors.Is(err, loop.ErrNeedsReconciliation) {
		t.Fatalf("got %v", err)
	}
	if err := loop.Reconcile(res.Record, "c1", loop.Reconciliation{Executed: "yes"}); err != nil {
		t.Fatal(err)
	}
	if loop.Assess(res.Record) != loop.ActionContinueAsNewTurn {
		t.Fatal("reconciled turn not continuable")
	}
	cont, _ := loop.PortableContinuation(res.Messages, res.Record)
	if !strings.Contains(*cont[len(cont)-1].Content, "did execute; its result is unavailable") {
		t.Fatalf("dishonest result: %q", *cont[len(cont)-1].Content)
	}
	if fx.executions() != 1 || len(ad.requests) != 1 {
		t.Fatal("harness re-ran work to recover")
	}
}

func TestCancellationBeforeSecondCall(t *testing.T) {
	fx := startFixture(t, "")
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(add("c1", "1", "1"), add("c2", "2", "2"))}}
	ctx, cancel := context.WithCancel(context.Background())
	approver := approveFunc(func(_ context.Context, c round.ToolCall) (bool, error) {
		if c.ID == "c2" {
			cancel()
		}
		return true, nil
	})
	res, _ := loop.RunTurn(ctx, ad, history, fx.client.Tools(), fx.client, approver, nil, loop.Options{})
	calls := res.Record.Rounds[0].Calls
	if calls[0].State != loop.CallCompleted || calls[1].State != loop.CallNotDispatched || fx.executions() != 1 {
		t.Fatalf("outcomes %+v executions %d", calls, fx.executions())
	}
	cont, err := loop.PortableContinuation(res.Messages, res.Record)
	if err != nil || len(cont) != 3 || !strings.HasPrefix(*cont[2].Content, "not executed") {
		t.Fatalf("continuation %+v %v", cont, err)
	}
}

type approveFunc func(context.Context, round.ToolCall) (bool, error)

func (f approveFunc) Approve(ctx context.Context, c round.ToolCall) (bool, error) { return f(ctx, c) }

func TestDispatchMarkPersistedBeforeCall(t *testing.T) {
	fx := startFixture(t, "")
	path := filepath.Join(t.TempDir(), "doc.json")
	doc := &convo.Document{Title: "t", Messages: history}
	rec := convo.NewFileRecorder(path, doc)
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(add("c1", "2", "2")), answer("4")}}
	var seen loop.CallState
	opt := loop.Options{AfterDispatchBeforeCall: func(round.ToolCall) {
		d, err := convo.Load(path)
		if err == nil {
			seen = d.Turns[0].Rounds[0].Calls[0].State
		}
		if fx.executions() != 0 {
			seen = "executed-too-early"
		}
	}}
	if _, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, rec, opt); err != nil {
		t.Fatal(err)
	}
	if seen != loop.CallDispatched {
		t.Fatalf("on disk before execution: %q", seen)
	}
	d, _ := convo.Load(path)
	if d.Turns[0].State != loop.StateComplete || d.Turns[0].Rounds[0].Calls[0].State != loop.CallCompleted {
		t.Fatalf("final record %+v", d.Turns[0])
	}
}

func TestForcedFinalAfterRepeatedFailures(t *testing.T) {
	fx := startFixture(t, "")
	bad := round.ToolCall{ID: "", Name: "add", Arguments: `{"a":"x","b":1}`}
	b1, b2 := bad, bad
	b1.ID, b2.ID = "c1", "c2"
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(b1), toolRound(b2), toolRound(add("c3", "1", "1"))}}
	res, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, nil, loop.Options{})
	if err == nil || ad.requests[2].Intent != round.IntentForcedFinal {
		t.Fatalf("err=%v intent=%v", err, ad.requests[2].Intent)
	}
	// the fixture rejected the bad args itself (tool error, not executed); the
	// forced-final tool request is never dispatched.
	if fx.executions() != 0 || res.Record.Reason != loop.ReasonProtocol {
		t.Fatalf("executions %d record %+v", fx.executions(), res.Record)
	}
}

func TestUnofferedToolNeverDispatched(t *testing.T) {
	fx := startFixture(t, "")
	ad := &scripted{steps: []func(round.Request) (*round.Final, error){toolRound(round.ToolCall{ID: "c1", Name: "rm_rf", Arguments: `{}`})}}
	res, err := loop.RunTurn(context.Background(), ad, history, fx.client.Tools(), fx.client, loop.ApproveAll{}, nil, loop.Options{})
	if err == nil || fx.executions() != 0 || res.Record.Rounds != nil {
		t.Fatalf("err=%v executions=%d", err, fx.executions())
	}
}
