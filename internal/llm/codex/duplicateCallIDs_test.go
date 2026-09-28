package codex

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/michaelquigley/pane/internal/llm"
)

// compatibleRound is one completed assistant round carrying a compatible
// envelope for call, followed by its tool result.
func compatibleRound(a *Adapter, call replayCall) []llm.Message {
	return replayRequest(a, "", []llm.ToolCall{portableCall(call)}, []json.RawMessage{callRaw(call)}, []llm.CallBinding{callBinding(call)}).Messages[1:]
}

// portableRound is the same round with no envelope, replayed portably.
func portableRound(call replayCall) []llm.Message {
	return []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{portableCall(call)}},
		{Role: "tool", ToolCallID: call.pane, Content: llm.StringContent("r:" + call.pane)},
	}
}

func historyRequest(rounds ...[]llm.Message) llm.RoundRequest {
	request := baseRequest()
	for _, round := range rounds {
		request.Messages = append(request.Messages, round...)
	}
	return request
}

// a pane call id repeated anywhere in history is rejected whichever replay
// path each round takes, before credential access or provider contact. the
// fixtures are synthetic history-consistency cases, not duplicate execution.
func TestDuplicateCallIDsRejectedOnEveryReplayPath(t *testing.T) {
	// the same pane id bound to a different provider call, so the rejection
	// cannot come from provider-id handling.
	sameID := replayB
	sameID.pane = replayA.pane
	cases := []struct {
		name   string
		rounds func(a *Adapter) [][]llm.Message
	}{
		{name: "compatible then compatible", rounds: func(a *Adapter) [][]llm.Message {
			return [][]llm.Message{compatibleRound(a, replayA), compatibleRound(a, sameID)}
		}},
		{name: "portable then portable", rounds: func(*Adapter) [][]llm.Message {
			return [][]llm.Message{portableRound(replayA), portableRound(sameID)}
		}},
		{name: "compatible then portable", rounds: func(a *Adapter) [][]llm.Message {
			return [][]llm.Message{compatibleRound(a, replayA), portableRound(sameID)}
		}},
		{name: "portable then compatible", rounds: func(a *Adapter) [][]llm.Message {
			return [][]llm.Message{portableRound(replayA), compatibleRound(a, sameID)}
		}},
		{name: "identical compatible round repeated", rounds: func(a *Adapter) [][]llm.Message {
			return [][]llm.Message{compatibleRound(a, replayA), compatibleRound(a, replayA)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, creds, requests := failOnContact(t)
			request := historyRequest(tc.rounds(a)...)
			// built separately from the same fixture, so it shares no mutable
			// data with the history under test.
			expected := historyRequest(tc.rounds(a)...).Messages
			_, err := a.Round(context.Background(), request, func(llm.RoundEvent) {})
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "invalid_request" {
				t.Fatalf("got %v, want invalid_request", err)
			}
			if *requests != 0 || creds.accesses != 0 {
				t.Fatalf("duplicate history contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
			}
			if !reflect.DeepEqual(request.Messages, expected) {
				t.Fatal("stored history changed")
			}
		})
	}
}

// the control: distinct pane ids replay on every path combination.
func TestDistinctCallIDsReplayOnEveryPath(t *testing.T) {
	a, creds, requests := failOnContact(t)
	combinations := [][][]llm.Message{
		{compatibleRound(a, replayA), compatibleRound(a, replayB)},
		{portableRound(replayA), portableRound(replayB)},
		{compatibleRound(a, replayA), portableRound(replayB)},
		{portableRound(replayA), compatibleRound(a, replayB)},
	}
	for i, rounds := range combinations {
		if _, err := a.buildRequest(historyRequest(rounds...)); err != nil {
			t.Fatalf("combination %d with distinct ids rejected: %v", i, err)
		}
	}
	if *requests != 0 || creds.accesses != 0 {
		t.Fatalf("request construction contacted provider: requests=%d accesses=%d", *requests, creds.accesses)
	}
}
