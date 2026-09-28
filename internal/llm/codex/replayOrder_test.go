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

// replayCall is one synthetic tool call described on both sides: its portable
// pane call and its provider function call item.
type replayCall struct {
	pane, callID, itemID, args string
}

var (
	replayA = replayCall{pane: "pane.a", callID: "call_A", itemID: "fc_A", args: `{"n":1}`}
	replayB = replayCall{pane: "pane.b", callID: "call_B", itemID: "fc_B", args: `{"n":2}`}
)

func reasoningRaw(id string) json.RawMessage {
	return json.RawMessage(`{"type":"reasoning","id":"` + id + `","status":"completed","summary":[],"encrypted_content":"ENC_` + id + `"}`)
}

func messageRaw(id, text string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "message", "id": id, "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}})
	return raw
}

func callRaw(c replayCall) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"type": "function_call", "id": c.itemID, "status": "completed", "call_id": c.callID, "name": "add", "arguments": c.args})
	return raw
}

func portableCall(c replayCall) llm.ToolCall {
	return llm.ToolCall{ID: c.pane, Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: c.args}}
}

func callBinding(c replayCall) llm.CallBinding {
	return llm.CallBinding{PaneCallID: c.pane, ProviderCallID: c.callID, ProviderItemID: c.itemID}
}

// failOnContact returns an adapter whose transport and credentials record
// every contact; request-construction tests assert both stay at zero.
func failOnContact(t *testing.T) (*Adapter, *fakeCredentials, *int) {
	t.Helper()
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	requests := new(int)
	a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) {
		*requests++
		return nil, errors.New("unexpected provider contact")
	}))
	return a, creds, requests
}

// replayRequest appends one assistant round carrying the given envelope and
// its tool results, in portable call order, to the base request.
func replayRequest(a *Adapter, content string, calls []llm.ToolCall, items []json.RawMessage, bindings []llm.CallBinding) llm.RoundRequest {
	request := baseRequest()
	assistant := llm.Message{
		Role:         "assistant",
		ToolCalls:    calls,
		Origin:       &llm.RoundOrigin{Alias: "sol", Identity: a.identity},
		Continuation: &llm.Continuation{Format: continuationFormat, Version: 1, Identity: a.identity, Items: items, Bindings: bindings},
	}
	if content != "" {
		assistant.Content = llm.StringContent(content)
	}
	request.Messages = append(request.Messages, assistant)
	for _, call := range calls {
		request.Messages = append(request.Messages, llm.Message{Role: "tool", ToolCallID: call.ID, Content: llm.StringContent("r:" + call.ID)})
	}
	return request
}

func snapshotContinuation(c *llm.Continuation) llm.Continuation {
	copied := *c
	copied.Items = make([]json.RawMessage, len(c.Items))
	for i, raw := range c.Items {
		copied.Items[i] = append(json.RawMessage(nil), raw...)
	}
	copied.Bindings = append([]llm.CallBinding(nil), c.Bindings...)
	return copied
}

func wireInput(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var wire struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Input
}

// replayedInput is the expected outgoing input for a compatible replay: the
// user message, the supplied items in order, then one output per call.
func replayedInput(items []json.RawMessage, calls []replayCall) []map[string]any {
	expected := []map[string]any{{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "add 2 and 2"}}}}
	for _, raw := range items {
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		expected = append(expected, item)
	}
	for _, c := range calls {
		expected = append(expected, map[string]any{"type": "function_call_output", "call_id": c.callID, "output": "r:" + c.pane})
	}
	return expected
}

// portableInput is the expected outgoing input for portable fallback: the
// user message, the assistant text when present, the calls in portable order
// under pane-owned ids, then their outputs in the same order.
func portableInput(content string, calls []replayCall) []map[string]any {
	expected := []map[string]any{{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "add 2 and 2"}}}}
	if content != "" {
		expected = append(expected, map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": content, "annotations": []any{}}}})
	}
	for _, c := range calls {
		expected = append(expected, map[string]any{"type": "function_call", "call_id": portableCallID(c.pane), "name": "add", "arguments": c.args})
	}
	for _, c := range calls {
		expected = append(expected, map[string]any{"type": "function_call_output", "call_id": portableCallID(c.pane), "output": "r:" + c.pane})
	}
	return expected
}

func TestReplayRequiresOrderedCallAgreement(t *testing.T) {
	sameArgsB := replayCall{pane: "pane.b", callID: "call_B", itemID: "fc_B", args: replayA.args}
	cases := []struct {
		name     string
		items    []replayCall
		portable []replayCall
		bindings []llm.CallBinding
		replay   bool
	}{
		{name: "agreeing order replays", items: []replayCall{replayA, replayB}, portable: []replayCall{replayA, replayB}, bindings: []llm.CallBinding{callBinding(replayA), callBinding(replayB)}, replay: true},
		{name: "items reordered", items: []replayCall{replayB, replayA}, portable: []replayCall{replayA, replayB}, bindings: []llm.CallBinding{callBinding(replayA), callBinding(replayB)}},
		{name: "portable calls and bindings reordered", items: []replayCall{replayA, replayB}, portable: []replayCall{replayB, replayA}, bindings: []llm.CallBinding{callBinding(replayB), callBinding(replayA)}},
		{name: "bindings reordered", items: []replayCall{replayA, replayB}, portable: []replayCall{replayA, replayB}, bindings: []llm.CallBinding{callBinding(replayB), callBinding(replayA)}},
		// identical names and arguments leave only the provider ids to
		// distinguish the calls; each binding must name the item at its own
		// position.
		{name: "provider ids cross-bound", items: []replayCall{replayA, sameArgsB}, portable: []replayCall{replayA, sameArgsB}, bindings: []llm.CallBinding{
			{PaneCallID: replayA.pane, ProviderCallID: sameArgsB.callID, ProviderItemID: sameArgsB.itemID},
			{PaneCallID: sameArgsB.pane, ProviderCallID: replayA.callID, ProviderItemID: replayA.itemID},
		}},
		{name: "duplicate provider call id", items: []replayCall{replayA, {pane: "pane.b", callID: replayA.callID, itemID: "fc_B", args: replayB.args}}, portable: []replayCall{replayA, replayB}, bindings: []llm.CallBinding{callBinding(replayA), {PaneCallID: replayB.pane, ProviderCallID: replayA.callID, ProviderItemID: "fc_B"}}},
		{name: "missing binding", items: []replayCall{replayA, replayB}, portable: []replayCall{replayA, replayB}, bindings: []llm.CallBinding{callBinding(replayA)}},
		{name: "extra call item", items: []replayCall{replayA, replayB}, portable: []replayCall{replayA}, bindings: []llm.CallBinding{callBinding(replayA)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, creds, requests := failOnContact(t)
			var items []json.RawMessage
			for _, c := range tc.items {
				items = append(items, callRaw(c))
			}
			var calls []llm.ToolCall
			for _, c := range tc.portable {
				calls = append(calls, portableCall(c))
			}
			request := replayRequest(a, "", calls, items, tc.bindings)
			stored := snapshotContinuation(request.Messages[1].Continuation)
			body, err := a.buildRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			want := portableInput("", tc.portable)
			if tc.replay {
				want = replayedInput(items, tc.portable)
			}
			if got := wireInput(t, body); !reflect.DeepEqual(got, want) {
				t.Fatalf("outgoing input wrong:\n got %#v\nwant %#v", got, want)
			}
			if !tc.replay && strings.Contains(string(body), `"fc_`) {
				t.Fatalf("invalid envelope leaked provider items: %s", body)
			}
			if !reflect.DeepEqual(*request.Messages[1].Continuation, stored) {
				t.Fatal("stored envelope changed")
			}
			if *requests != 0 || creds.accesses != 0 {
				t.Fatalf("request construction contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
			}
		})
	}
}

func TestReplayReasoningShapeAcceptance(t *testing.T) {
	cases := []struct {
		name    string
		items   []json.RawMessage
		content string
		calls   []replayCall
		replay  bool
	}{
		{name: "reasoning then call", items: []json.RawMessage{reasoningRaw("rs_1"), callRaw(replayA)}, calls: []replayCall{replayA}, replay: true},
		{name: "reasoning then message", items: []json.RawMessage{reasoningRaw("rs_1"), messageRaw("msg_1", "done")}, content: "done", replay: true},
		{name: "interleaved reasoning and calls", items: []json.RawMessage{reasoningRaw("rs_1"), callRaw(replayA), reasoningRaw("rs_2"), callRaw(replayB)}, calls: []replayCall{replayA, replayB}, replay: true},
		{name: "reasoning message call", items: []json.RawMessage{reasoningRaw("rs_1"), messageRaw("msg_1", "done"), callRaw(replayA)}, content: "done", calls: []replayCall{replayA}, replay: true},
		// consecutive reasoning items are accepted: no evidence establishes
		// an immediate non-reasoning successor rule.
		{name: "consecutive reasoning then call", items: []json.RawMessage{reasoningRaw("rs_1"), reasoningRaw("rs_2"), callRaw(replayA)}, calls: []replayCall{replayA}, replay: true},
		{name: "trailing reasoning item", items: []json.RawMessage{callRaw(replayA), reasoningRaw("rs_1")}, calls: []replayCall{replayA}},
		{name: "trailing reasoning after calls", items: []json.RawMessage{reasoningRaw("rs_1"), callRaw(replayA), reasoningRaw("rs_2")}, calls: []replayCall{replayA}},
		{name: "trailing reasoning run", items: []json.RawMessage{messageRaw("msg_1", "done"), reasoningRaw("rs_1"), reasoningRaw("rs_2")}, content: "done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, creds, requests := failOnContact(t)
			var calls []llm.ToolCall
			var bindings []llm.CallBinding
			for _, c := range tc.calls {
				calls = append(calls, portableCall(c))
				bindings = append(bindings, callBinding(c))
			}
			request := replayRequest(a, tc.content, calls, tc.items, bindings)
			stored := snapshotContinuation(request.Messages[1].Continuation)
			body, err := a.buildRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			want := portableInput(tc.content, tc.calls)
			if tc.replay {
				want = replayedInput(tc.items, tc.calls)
			}
			if got := wireInput(t, body); !reflect.DeepEqual(got, want) {
				t.Fatalf("outgoing input wrong:\n got %#v\nwant %#v", got, want)
			}
			if !reflect.DeepEqual(*request.Messages[1].Continuation, stored) {
				t.Fatal("stored envelope changed")
			}
			if *requests != 0 || creds.accesses != 0 {
				t.Fatalf("request construction contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
			}
		})
	}
}

// completedStream streams each item as added/done at its output index and
// closes with a completed terminal repeating the same output.
func completedStream(items []json.RawMessage) string {
	var stream strings.Builder
	for index, raw := range items {
		for _, kind := range []string{"response.output_item.added", "response.output_item.done"} {
			fmt.Fprintf(&stream, "data: {\"type\":%q,\"output_index\":%d,\"item\":%s}\n\n", kind, index, raw)
		}
	}
	terminal, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": items}})
	stream.WriteString("data: " + string(terminal) + "\n\n")
	return stream.String()
}

func TestCapturedReasoningShapesReplayInProviderOrder(t *testing.T) {
	cases := []struct {
		name    string
		items   []json.RawMessage
		content string
	}{
		{name: "interleaved reasoning and calls", items: []json.RawMessage{reasoningRaw("rs_1"), callRaw(replayA), reasoningRaw("rs_2"), callRaw(replayB)}},
		{name: "consecutive reasoning then call", items: []json.RawMessage{reasoningRaw("rs_1"), reasoningRaw("rs_2"), callRaw(replayA)}},
		{name: "reasoning message call", items: []json.RawMessage{reasoningRaw("rs_1"), messageRaw("msg_1", "done"), callRaw(replayA)}, content: "done"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			final, err := parseStream(context.Background(), strings.NewReader(completedStream(tc.items)), func(llm.RoundEvent) {}, testIdentity(), "sol", "medium", 0)
			if err != nil {
				t.Fatalf("capture rejected provider shape: %v", err)
			}
			if final.Continuation == nil || len(final.Continuation.Items) != len(tc.items) {
				t.Fatalf("continuation lost: %+v", final.Continuation)
			}
			for i, raw := range tc.items {
				if string(final.Continuation.Items[i]) != string(raw) {
					t.Fatalf("captured item %d not verbatim or out of order:\n got %s\nwant %s", i, final.Continuation.Items[i], raw)
				}
			}
			a, creds, requests := failOnContact(t)
			var calls []llm.ToolCall
			var expected []replayCall
			for i, call := range final.Calls {
				calls = append(calls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.ToolCallFunction{Name: call.Name, Arguments: call.Arguments}})
				expected = append(expected, replayCall{pane: call.ID, callID: final.Continuation.Bindings[i].ProviderCallID})
			}
			request := replayRequest(a, tc.content, calls, final.Continuation.Items, final.Continuation.Bindings)
			request.Messages[1].Origin = final.Origin
			body, err := a.buildRequest(request)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := wireInput(t, body), replayedInput(tc.items, expected); !reflect.DeepEqual(got, want) {
				t.Fatalf("captured envelope did not replay in provider order:\n got %#v\nwant %#v", got, want)
			}
			if *requests != 0 || creds.accesses != 0 {
				t.Fatalf("request construction contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
			}
		})
	}
}

func TestCaptureRejectsTrailingReasoning(t *testing.T) {
	for name, items := range map[string][]json.RawMessage{
		"trailing reasoning item": {callRaw(replayA), reasoningRaw("rs_1")},
		"trailing reasoning run":  {messageRaw("msg_1", "done"), reasoningRaw("rs_1"), reasoningRaw("rs_2")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseStream(context.Background(), strings.NewReader(completedStream(items)), func(llm.RoundEvent) {}, testIdentity(), "sol", "medium", 0)
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
				t.Fatalf("got %v, want protocol error", err)
			}
		})
	}
}

func TestInvalidEnvelopeAndPortableHistoryFailBeforeContact(t *testing.T) {
	reordered := []json.RawMessage{callRaw(replayB), callRaw(replayA)}
	bindings := []llm.CallBinding{callBinding(replayA), callBinding(replayB)}
	cases := map[string][]llm.ToolCall{
		"non-object portable arguments": {portableCall(replayA), {ID: replayB.pane, Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: "[]"}}},
		"duplicate portable call ids":   {portableCall(replayA), {ID: replayA.pane, Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: replayB.args}}},
	}
	for name, calls := range cases {
		t.Run(name, func(t *testing.T) {
			a, creds, requests := failOnContact(t)
			request := replayRequest(a, "", calls, reordered, bindings)
			stored := snapshotContinuation(request.Messages[1].Continuation)
			_, err := a.Round(context.Background(), request, func(llm.RoundEvent) {})
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "invalid_request" {
				t.Fatalf("got %v, want invalid_request", err)
			}
			if *requests != 0 || creds.accesses != 0 {
				t.Fatalf("invalid history contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
			}
			if !reflect.DeepEqual(*request.Messages[1].Continuation, stored) {
				t.Fatal("stored envelope changed")
			}
		})
	}
}
