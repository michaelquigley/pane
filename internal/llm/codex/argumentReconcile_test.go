package codex

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/internal/llm"
)

// argument reconciliation fixtures are synthetic: the inconsistencies they
// pin were demonstrated against the parser, not observed from the provider.

func argsAdded(index int, id string) string {
	return fmt.Sprintf("data: {\"type\":\"response.output_item.added\",\"output_index\":%d,\"item\":{\"type\":\"function_call\",\"id\":\"fc_%s\",\"call_id\":\"call_%s\",\"name\":\"add\"}}\n\n", index, id, id)
}

func argsDelta(index int, delta string) string {
	return fmt.Sprintf("data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":%d,\"delta\":%q}\n\n", index, delta)
}

func argsDone(index int, arguments string) string {
	return fmt.Sprintf("data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":%d,\"arguments\":%q}\n\n", index, arguments)
}

func argsCallItem(id, arguments string) string {
	return fmt.Sprintf("{\"type\":\"function_call\",\"id\":\"fc_%s\",\"call_id\":\"call_%s\",\"name\":\"add\",\"status\":\"completed\",\"arguments\":%q}", id, id, arguments)
}

func argsItemDone(index int, id, arguments string) string {
	return fmt.Sprintf("data: {\"type\":\"response.output_item.done\",\"output_index\":%d,\"item\":%s}\n\n", index, argsCallItem(id, arguments))
}

func argsTerminal(items ...string) string {
	return "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[" + strings.Join(items, ",") + "]}}\n\n"
}

// runArgsLoop drives one streamed round through the shared tool loop and
// reports how many tools executed and how many requests were made.
func runArgsLoop(t *testing.T, stream string) (int, int, error) {
	t.Helper()
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	requests := 0
	a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return sseResponse(stream), nil
		}
		return sseResponse(responseText("4")), nil
	}))
	executor := &fakeExecutor{}
	request := baseRequest()
	err := llm.RunToolLoop(context.Background(), a, llm.Turn{}, request.Messages, request.Model, 0, request.Tools, executor, loopSink(func(llm.LoopEvent) error { return nil }), nil)
	return executor.calls, requests, err
}

func TestArgumentReconciliationAccepted(t *testing.T) {
	cases := []struct {
		name   string
		stream string
		calls  int
	}{
		{name: "consistent partial completed by backfill", stream: argsAdded(0, "A") + argsDelta(0, `{"x":`) + argsTerminal(argsCallItem("A", `{"x":2}`)), calls: 1},
		{name: "argument-only partial completed by backfill", stream: argsDelta(0, `{"x":`) + argsTerminal(argsCallItem("A", `{"x":2}`)), calls: 1},
		{name: "explicit finalization matches backfill", stream: argsAdded(0, "A") + argsDone(0, `{"x":2}`) + argsTerminal(argsCallItem("A", `{"x":2}`)), calls: 1},
		{name: "streamed completion with full deltas", stream: argsAdded(0, "A") + argsDelta(0, `{"x":`) + argsDelta(0, `2}`) + argsDone(0, `{"x":2}`) + argsItemDone(0, "A", `{"x":2}`) + argsTerminal(), calls: 1},
		{name: "streamed completion with no argument events", stream: argsAdded(0, "A") + argsItemDone(0, "A", `{"x":2}`) + argsTerminal(), calls: 1},
		{name: "two calls both consistent", stream: argsAdded(0, "A") + argsItemDone(0, "A", `{"x":1}`) + argsAdded(1, "B") + argsDelta(1, `{"x"`) + argsTerminal(argsCallItem("A", `{"x":1}`), argsCallItem("B", `{"x":2}`)), calls: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseStream(context.Background(), strings.NewReader(tc.stream), func(llm.RoundEvent) {}, testIdentity(), "sol", "medium", 0); err != nil {
				t.Fatalf("consistent round rejected: %v", err)
			}
			calls, requests, err := runArgsLoop(t, tc.stream)
			if err != nil || calls != tc.calls || requests != 2 {
				t.Fatalf("consistent round: err=%v executions=%d requests=%d, want %d executions over 2 requests", err, calls, requests, tc.calls)
			}
		})
	}
}

func TestArgumentReconciliationRejected(t *testing.T) {
	changed, conflicting, malformed := "output item arguments changed", "conflicting finalized arguments", "malformed arguments completion"
	cases := []struct {
		name   string
		stream string
		reason string
	}{
		// terminal backfill path.
		{name: "contradictory partial backfill", stream: argsAdded(0, "A") + argsDelta(0, `{"x":1`) + argsTerminal(argsCallItem("A", `{"x":2}`)), reason: changed},
		{name: "contradictory argument-only backfill", stream: argsDelta(0, `{"x":1`) + argsTerminal(argsCallItem("A", `{"x":2}`)), reason: changed},
		{name: "finalized arguments disagree with backfill", stream: argsAdded(0, "A") + argsDone(0, `{"x":1}`) + argsTerminal(argsCallItem("A", `{"x":2}`)), reason: changed},
		{name: "explicit empty finalization disagrees with backfill", stream: argsAdded(0, "A") + argsDone(0, "") + argsTerminal(argsCallItem("A", `{}`)), reason: changed},
		// streamed completion path.
		{name: "deltas disagree with completed item", stream: argsAdded(0, "A") + argsDelta(0, `{"x":`) + argsItemDone(0, "A", `{"x":2}`) + argsTerminal(), reason: changed},
		{name: "explicit empty finalization disagrees with completed item", stream: argsAdded(0, "A") + argsDone(0, "") + argsItemDone(0, "A", `{}`) + argsTerminal(), reason: changed},
		// local finalization guards.
		{name: "repeated finalization conflicts", stream: argsAdded(0, "A") + argsDone(0, `{"x":1}`) + argsDone(0, `{"x":2}`) + argsTerminal(argsCallItem("A", `{"x":2}`)), reason: conflicting},
		{name: "finalization missing arguments", stream: argsAdded(0, "A") + "data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0}\n\n" + argsTerminal(argsCallItem("A", `{}`)), reason: malformed},
		{name: "finalization null arguments", stream: argsAdded(0, "A") + "data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"arguments\":null}\n\n" + argsTerminal(argsCallItem("A", `{}`)), reason: malformed},
		// argument-only slots cannot drop out of reconciliation.
		{name: "unresolved argument-only slot with empty output", stream: argsAdded(0, "A") + argsItemDone(0, "A", `{"x":1}`) + argsDelta(1, `{"x":`) + argsTerminal(), reason: "streamed output item never completed"},
		{name: "unresolved argument-only slot beyond terminal output", stream: argsAdded(0, "A") + argsItemDone(0, "A", `{"x":1}`) + argsDelta(1, `{"x":`) + argsTerminal(argsCallItem("A", `{"x":1}`)), reason: "streamed output item missing from terminal output"},
		// one contradictory call rejects the whole round.
		{name: "mixed round", stream: argsAdded(0, "A") + argsItemDone(0, "A", `{"x":1}`) + argsAdded(1, "B") + argsDelta(1, `{"x":1`) + argsTerminal(argsCallItem("A", `{"x":1}`), argsCallItem("B", `{"x":2}`)), reason: changed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStream(context.Background(), strings.NewReader(tc.stream), func(llm.RoundEvent) {}, testIdentity(), "sol", "medium", 0)
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" || roundErr.Reason != tc.reason {
				t.Fatalf("got %v, want protocol error %q", err, tc.reason)
			}
			calls, requests, err := runArgsLoop(t, tc.stream)
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
				t.Fatalf("loop got %v, want protocol error", err)
			}
			if calls != 0 || requests != 1 {
				t.Fatalf("rejected round executed %d tools over %d requests", calls, requests)
			}
		})
	}
}
