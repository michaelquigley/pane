package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/sse"
)

// the encrypted payload carries characters that JSON escapes or that a
// careless re-encoding would mangle; string values must survive by code point.
const continuationEventSecret = "gAAAAB+/=<ENC>&é \"quoted\""

func continuationEventAssistant() llm.Message {
	identity := llm.RoundIdentity{Provider: "openai-codex", Protocol: "responses", UpstreamModel: "gpt-5.6-sol", Service: "https://example.invalid/codex", AccountScope: "chatgpt:synthetic"}
	secret, _ := json.Marshal(continuationEventSecret)
	items := []json.RawMessage{
		json.RawMessage(`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":` + string(secret) + `,"future_field":{"nested":[1,"x",null]}}`),
		json.RawMessage(`{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"adding"}]}`),
		json.RawMessage(`{"type":"reasoning","id":"rs_2","summary":[],"encrypted_content":` + string(secret) + `}`),
		json.RawMessage(`{"type":"function_call","id":"fc_1","call_id":"call_1","name":"add","status":"completed","arguments":"{\"a\":2}","future_flag":true}`),
	}
	return llm.Message{
		Role:      "assistant",
		Content:   llm.StringContent("adding"),
		ToolCalls: []llm.ToolCall{{ID: "pane_1", Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: `{"a":2}`}}},
		Origin:    &llm.RoundOrigin{Alias: "sol", Identity: identity, RequestedEffort: "medium"},
		Continuation: &llm.Continuation{
			Format: "codex-responses-items", Version: 1, Identity: identity, Items: items,
			Bindings: []llm.CallBinding{{PaneCallID: "pane_1", ProviderCallID: "call_1", ProviderItemID: "fc_1"}},
		},
	}
}

// the continuation reaches the browser through the real round_complete SSE
// payload, not a standalone dd.Unbind of the message: nested custom-marshaler
// dispatch differs from the top-level path, and only this path is shipped.
func TestRoundCompleteContinuationReloads(t *testing.T) {
	original := continuationEventAssistant()
	recorder := httptest.NewRecorder()
	writer, err := sse.NewWriter(recorder)
	if err != nil {
		t.Fatal(err)
	}
	if err := (chatEventSink{writer: writer}).Emit(llm.LoopEvent{Kind: llm.LoopRoundComplete, Round: &llm.LoopRound{Assistant: original}}); err != nil {
		t.Fatal(err)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "event: round_complete\ndata: ") {
		t.Fatalf("unexpected SSE framing: %q", body)
	}
	data := strings.TrimSpace(strings.TrimPrefix(body, "event: round_complete\ndata: "))

	var event struct {
		Assistant json.RawMessage `json:"assistant"`
	}
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		t.Fatal(err)
	}
	var emitted struct {
		Continuation struct {
			Items []any `json:"items"`
		} `json:"continuation"`
	}
	if err := json.Unmarshal(event.Assistant, &emitted); err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"rs_1", "msg_1", "rs_2", "fc_1"}
	if len(emitted.Continuation.Items) != len(wantIDs) {
		t.Fatalf("emitted %d items, want %d: %s", len(emitted.Continuation.Items), len(wantIDs), data)
	}
	for i, element := range emitted.Continuation.Items {
		object, ok := element.(map[string]any)
		if !ok {
			t.Fatalf("emitted item %d is %T, want a JSON object: %s", i, element, data)
		}
		if object["id"] != wantIDs[i] {
			t.Fatalf("emitted item %d is %v, want %s: order not preserved", i, object["id"], wantIDs[i])
		}
	}

	// the emitted assistant binds back through the same intake /api/chat uses.
	var request chatRequest
	intake := []byte(`{"model":"sol","messages":[` + string(event.Assistant) + `]}`)
	if err := dd.BindJSONReader(&request, bytes.NewReader(intake)); err != nil {
		t.Fatalf("emitted assistant does not reload: %v", err)
	}
	if len(request.Messages) != 1 {
		t.Fatalf("reloaded %d messages", len(request.Messages))
	}
	restored := request.Messages[0]
	if restored.Origin == nil || !reflect.DeepEqual(*restored.Origin, *original.Origin) {
		t.Fatalf("origin lost: %+v", restored.Origin)
	}
	if !reflect.DeepEqual(restored.ToolCalls, original.ToolCalls) {
		t.Fatalf("tool calls lost: %+v", restored.ToolCalls)
	}
	envelope := restored.Continuation
	if envelope == nil || envelope.Format != original.Continuation.Format || envelope.Version != 1 {
		t.Fatalf("envelope lost: %+v", envelope)
	}
	if envelope.Identity != original.Continuation.Identity {
		t.Fatalf("identity lost: %+v", envelope.Identity)
	}
	if !reflect.DeepEqual(envelope.Bindings, original.Continuation.Bindings) {
		t.Fatalf("bindings lost: %+v", envelope.Bindings)
	}
	if len(envelope.Items) != len(wantIDs) {
		t.Fatalf("reloaded %d items", len(envelope.Items))
	}
	for i, raw := range envelope.Items {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("reloaded item %d is not a JSON object: %v", i, err)
		}
		if item["id"] != wantIDs[i] {
			t.Fatalf("reloaded item %d is %v, want %s", i, item["id"], wantIDs[i])
		}
		if strings.HasPrefix(wantIDs[i], "rs_") && item["encrypted_content"] != continuationEventSecret {
			t.Fatalf("encrypted content changed in item %d: %q", i, item["encrypted_content"])
		}
	}
	var first, last map[string]any
	_ = json.Unmarshal(envelope.Items[0], &first)
	_ = json.Unmarshal(envelope.Items[3], &last)
	if !reflect.DeepEqual(first["future_field"], map[string]any{"nested": []any{float64(1), "x", nil}}) || last["future_flag"] != true {
		t.Fatalf("unknown opaque-item fields lost: %s / %s", envelope.Items[0], envelope.Items[3])
	}
}
