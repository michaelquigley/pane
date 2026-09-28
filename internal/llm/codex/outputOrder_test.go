package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/internal/llm"
)

// streamedItem is one output item streamed as added/done at its output index.
type streamedItem struct {
	index int
	raw   json.RawMessage
}

// terminalOrderStream streams the given items and closes with a completed
// terminal event carrying output, which may include items never streamed.
func terminalOrderStream(streamed []streamedItem, output []json.RawMessage) string {
	var stream strings.Builder
	for _, s := range streamed {
		for _, kind := range []string{"response.output_item.added", "response.output_item.done"} {
			fmt.Fprintf(&stream, "data: {\"type\":%q,\"output_index\":%d,\"item\":%s}\n\n", kind, s.index, s.raw)
		}
	}
	terminal, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}})
	stream.WriteString("data: " + string(terminal) + "\n\n")
	return stream.String()
}

func TestTerminalOutputOrderAssembly(t *testing.T) {
	rs1, rs2, msg := reasoningRaw("rs_1"), reasoningRaw("rs_2"), messageRaw("msg_1", "done")
	fcA, fcB := callRaw(replayA), callRaw(replayB)
	cases := []struct {
		name     string
		streamed []streamedItem
		output   []json.RawMessage
		content  string
		calls    []replayCall
	}{
		{name: "terminal-only reasoning before streamed call", streamed: []streamedItem{{1, fcA}}, output: []json.RawMessage{rs1, fcA}, calls: []replayCall{replayA}},
		{name: "terminal-only message before streamed call", streamed: []streamedItem{{1, fcA}}, output: []json.RawMessage{msg, fcA}, content: "done", calls: []replayCall{replayA}},
		{name: "terminal-only message between streamed items", streamed: []streamedItem{{0, rs1}, {2, fcA}}, output: []json.RawMessage{rs1, msg, fcA}, content: "done", calls: []replayCall{replayA}},
		{name: "terminal-only reasoning between streamed calls", streamed: []streamedItem{{0, fcA}, {2, fcB}}, output: []json.RawMessage{fcA, rs2, fcB}, calls: []replayCall{replayA, replayB}},
		{name: "terminal-only output with no streamed items", output: []json.RawMessage{rs1, fcA, msg}, content: "done", calls: []replayCall{replayA}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previews := make(map[int]string)
			emit := func(event llm.RoundEvent) {
				if event.Kind == "tool_call_start" {
					previews[event.Call.Index] = event.Call.ID
				}
			}
			final, err := parseStream(context.Background(), strings.NewReader(terminalOrderStream(tc.streamed, tc.output)), emit, testIdentity(), "sol", "medium", 0)
			if err != nil {
				t.Fatalf("valid terminal order rejected: %v", err)
			}
			if final.Content != tc.content || final.Continuation == nil || len(final.Continuation.Items) != len(tc.output) {
				t.Fatalf("round assembled wrong: %+v", final)
			}
			for i, raw := range tc.output {
				if string(final.Continuation.Items[i]) != string(raw) {
					t.Fatalf("item %d not in terminal order or not verbatim:\n got %s\nwant %s", i, final.Continuation.Items[i], raw)
				}
			}
			if len(final.Calls) != len(tc.calls) || len(final.Continuation.Bindings) != len(tc.calls) {
				t.Fatalf("calls or bindings lost: %+v", final)
			}
			for k, call := range final.Calls {
				position := -1
				for i, raw := range tc.output {
					if string(raw) == string(callRaw(tc.calls[k])) {
						position = i
					}
				}
				if call.Index != position || !strings.HasSuffix(call.ID, fmt.Sprintf("_%d", position)) {
					t.Fatalf("call %d does not carry its output index %d: %+v", k, position, call)
				}
				if preview, ok := previews[call.Index]; ok && preview != call.ID {
					t.Fatalf("preview id %q disagrees with finalized call id %q", preview, call.ID)
				}
				binding := final.Continuation.Bindings[k]
				if binding.PaneCallID != call.ID || binding.ProviderCallID != tc.calls[k].callID || binding.ProviderItemID != tc.calls[k].itemID {
					t.Fatalf("binding %d disagrees with finalized call: %+v vs %+v", k, binding, call)
				}
			}
			for _, s := range tc.streamed {
				var probe outputItem
				if probe, err = decodeItem(s.raw); err != nil {
					t.Fatal(err)
				}
				if probe.Type == "function_call" && previews[s.index] == "" {
					t.Fatalf("streamed call at index %d emitted no preview", s.index)
				}
			}

			// the assembled envelope replays in terminal order.
			a, creds, requests := failOnContact(t)
			var calls []llm.ToolCall
			var expected []replayCall
			for k, call := range final.Calls {
				calls = append(calls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.ToolCallFunction{Name: call.Name, Arguments: call.Arguments}})
				expected = append(expected, replayCall{pane: call.ID, callID: tc.calls[k].callID})
			}
			request := replayRequest(a, tc.content, calls, final.Continuation.Items, final.Continuation.Bindings)
			request.Messages[1].Origin = final.Origin
			body, err := a.buildRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			got, want := wireInput(t, body), replayedInput(tc.output, expected)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("assembled envelope did not replay in terminal order:\n got %#v\nwant %#v", got, want)
			}
			if *requests != 0 || creds.accesses != 0 {
				t.Fatalf("request construction contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
			}
		})
	}
}

func TestTerminalOutputOrderContradictionsRejected(t *testing.T) {
	rs1, msg := reasoningRaw("rs_1"), messageRaw("msg_1", "done")
	fcA := callRaw(replayA)
	missing, position := "streamed output item missing from terminal output", "streamed output item position disagrees with terminal output"
	// a started call with no done event, absent from the terminal output.
	startedOnly := "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"fc_B\",\"call_id\":\"call_B\",\"name\":\"add\"}}\n\n"
	cases := []struct {
		name   string
		stream string
		reason string
	}{
		{name: "streamed item missing", stream: terminalOrderStream([]streamedItem{{0, fcA}, {1, msg}}, []json.RawMessage{fcA}), reason: missing},
		{name: "started item missing", stream: startedOnly + terminalOrderStream([]streamedItem{{0, fcA}}, []json.RawMessage{fcA}), reason: missing},
		{name: "reversed order", stream: terminalOrderStream([]streamedItem{{0, rs1}, {1, fcA}}, []json.RawMessage{fcA, rs1}), reason: position},
		{name: "single matched item at wrong index", stream: terminalOrderStream([]streamedItem{{0, fcA}}, []json.RawMessage{rs1, fcA}), reason: position},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStream(context.Background(), strings.NewReader(tc.stream), func(llm.RoundEvent) {}, testIdentity(), "sol", "medium", 0)
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" || roundErr.Reason != tc.reason {
				t.Fatalf("got %v, want protocol error %q", err, tc.reason)
			}

			// the shared loop executes nothing from the rejected round.
			creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
			requests := 0
			a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return sseResponse(tc.stream), nil
			}))
			executor := &fakeExecutor{}
			request := baseRequest()
			err = llm.RunToolLoop(context.Background(), a, request.Messages, request.Model, 0, request.Tools, executor, loopSink(func(llm.LoopEvent) error { return nil }), nil)
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
				t.Fatalf("loop got %v, want protocol error", err)
			}
			if executor.calls != 0 || requests != 1 {
				t.Fatalf("rejected round executed %d tools over %d requests", executor.calls, requests)
			}
		})
	}
}

// the positive control for the zero-execution assertions above: the same
// loop executes a call from a valid terminal-ordered round exactly once.
func TestTerminalOutputOrderValidRoundExecutes(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	stream := terminalOrderStream([]streamedItem{{1, callRaw(replayA)}}, []json.RawMessage{reasoningRaw("rs_1"), callRaw(replayA)})
	requests := 0
	a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			return sseResponse(stream), nil
		}
		return sseResponse(responseText("3")), nil
	}))
	executor := &fakeExecutor{}
	request := baseRequest()
	if err := llm.RunToolLoop(context.Background(), a, request.Messages, request.Model, 0, request.Tools, executor, loopSink(func(llm.LoopEvent) error { return nil }), nil); err != nil {
		t.Fatal(err)
	}
	if executor.calls != 1 || requests != 2 {
		t.Fatalf("valid round executed %d tools over %d requests", executor.calls, requests)
	}
}

// with empty terminal output the streamed items stand as the round; call ids
// still derive from the output index, so a sparse index cannot make the
// finalized id disagree with its preview.
func TestEmptyTerminalOutputKeepsPreviewIDs(t *testing.T) {
	previews := make(map[int]string)
	emit := func(event llm.RoundEvent) {
		if event.Kind == "tool_call_start" {
			previews[event.Call.Index] = event.Call.ID
		}
	}
	stream := terminalOrderStream([]streamedItem{{0, reasoningRaw("rs_1")}, {2, callRaw(replayA)}}, []json.RawMessage{})
	final, err := parseStream(context.Background(), strings.NewReader(stream), emit, testIdentity(), "sol", "medium", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.Calls) != 1 || final.Calls[0].Index != 2 || previews[2] == "" || final.Calls[0].ID != previews[2] || final.Continuation.Bindings[0].PaneCallID != previews[2] {
		t.Fatalf("finalized call disagrees with preview %q: %+v", previews[2], final)
	}
}
