package codex

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/internal/llm"
)

// a restart is simulated by giving each process its own namespace and a
// counter starting from zero; resetting only the counter within one process
// keeps the namespace and does not model a restart.
func TestCallIDsDistinctAcrossProcessNamespaces(t *testing.T) {
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(callNamespace) {
		t.Fatalf("process namespace %q is not 16 lowercase hex digits", callNamespace)
	}
	namespace, sequence := callNamespace, roundSequence.Load()
	defer func() { callNamespace = namespace; roundSequence.Store(sequence) }()

	process := func(ns string) llm.RoundFinal {
		t.Helper()
		callNamespace = ns
		roundSequence.Store(0)
		previews := make(map[int]string)
		emit := func(event llm.RoundEvent) {
			if event.Kind == "tool_call_start" {
				previews[event.Call.Index] = event.Call.ID
			}
		}
		final, err := parseStream(context.Background(), strings.NewReader(toolFixture(t)), emit, testIdentity(), "sol", "medium", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(final.Calls) != 1 || final.Continuation == nil || len(final.Continuation.Bindings) != 1 {
			t.Fatalf("fixture round assembled wrong: %+v", final)
		}
		call := final.Calls[0]
		if previews[call.Index] != call.ID || final.Continuation.Bindings[0].PaneCallID != call.ID {
			t.Fatalf("preview %q, binding %q, and call %q disagree", previews[call.Index], final.Continuation.Bindings[0].PaneCallID, call.ID)
		}
		return final
	}
	before := process("0000000000000001")
	after := process("0000000000000002")

	// a call stored before the namespace existed, in the old id format.
	legacy := "pane_codex_1_0_1"
	saved := []string{legacy, before.Calls[0].ID, after.Calls[0].ID}
	if saved[1] == saved[2] || saved[1] == legacy || saved[2] == legacy {
		t.Fatalf("call ids repeat across processes: %q", saved)
	}

	// the combined history converts to portable input with every stored id
	// kept as saved and each call paired with its own result.
	a, creds, requests := failOnContact(t)
	request := baseRequest()
	for _, id := range saved {
		request.Messages = append(request.Messages,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: id, Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: `{"a":2,"b":2}`}}}},
			llm.Message{Role: "tool", ToolCallID: id, Content: llm.StringContent("4")},
		)
	}
	body, err := a.buildRequest(request)
	if err != nil {
		t.Fatalf("combined pre/post-restart history rejected: %v", err)
	}
	var calls, outputs []any
	for _, item := range wireInput(t, body) {
		switch item["type"] {
		case "function_call":
			calls = append(calls, item["call_id"])
		case "function_call_output":
			outputs = append(outputs, item["call_id"])
		}
	}
	if len(calls) != len(saved) || len(outputs) != len(saved) {
		t.Fatalf("portable input lost calls or results: calls=%v outputs=%v", calls, outputs)
	}
	for i, id := range saved {
		if calls[i] != portableCallID(id) || outputs[i] != portableCallID(id) {
			t.Fatalf("call %d not paired under its saved id %q: call=%v output=%v", i, id, calls[i], outputs[i])
		}
		if request.Messages[1+2*i].ToolCalls[0].ID != id {
			t.Fatalf("saved id %q changed during conversion", id)
		}
	}
	if *requests != 0 || creds.accesses != 0 {
		t.Fatalf("request construction contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
	}
}
