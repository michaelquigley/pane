package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/michaelquigley/pane/internal/config"
)

// continuationHistory is a subscription tool round followed by its result.
// the envelope is built from items, which may be malformed.
func continuationHistory(items ...string) []Message {
	identity := RoundIdentity{Provider: "openai-codex", Protocol: "responses", UpstreamModel: "gpt-5.6-sol", Service: "https://example.invalid/codex"}
	raw := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		raw = append(raw, json.RawMessage(item))
	}
	return []Message{
		{Role: "user", Content: StringContent("add")},
		{
			Role:      "assistant",
			Content:   StringContent("adding"),
			ToolCalls: []ToolCall{{ID: "pane_1", Type: "function", Function: ToolCallFunction{Name: "add", Arguments: `{"a":2}`}}},
			Origin:    &RoundOrigin{Alias: "sol", Identity: identity},
			Continuation: &Continuation{
				Format: "codex-responses-items", Version: 1, Identity: identity, Items: raw,
				Bindings: []CallBinding{{PaneCallID: "pane_1", ProviderCallID: "call_1", ProviderItemID: "fc_1"}},
			},
		},
		{Role: "tool", Content: StringContent("4"), ToolCallID: "pane_1"},
	}
}

// generic and qwen upstream requests never carry pane's durable round record,
// and an unusable stored envelope cannot block them: the record is cleared on
// a wire-only copy before it is serialized, and the history is not touched.
func TestChatRequestsExcludeOriginAndContinuation(t *testing.T) {
	valid := []string{`{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"}`, `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"add","arguments":"{\"a\":2}"}`}
	malformed := []string{`not json`, `{"type":"function_call","id":"fc_1"`}
	histories := map[string][]string{"valid envelope": valid, "malformed envelope": malformed}
	clients := map[string]func(string) (*Client, error){
		"generic": func(url string) (*Client, error) { return NewClient(url, "m", "", false), nil },
		"qwen ninfer": func(url string) (*Client, error) {
			return NewQwenClient(url, "m", "", config.ProfileNinfer, "low", false)
		},
		"qwen llamacpp": func(url string) (*Client, error) {
			return NewQwenClient(url, "m", "", config.ProfileLlamaCPP, "low", false)
		},
	}
	for historyName, items := range histories {
		for clientName, build := range clients {
			t.Run(historyName+"/"+clientName, func(t *testing.T) {
				history := continuationHistory(items...)
				// built separately from the same fixture, so it shares no
				// mutable data with the history under test.
				expected := continuationHistory(items...)
				var body map[string]any
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, qwenTextStream())
				}))
				defer server.Close()
				client, err := build(server.URL)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := client.Round(context.Background(), RoundRequest{Model: "m", Intent: IntentFinal, Messages: history}, func(RoundEvent) {}); err != nil {
					t.Fatalf("request with stored envelope failed: %v", err)
				}
				messages, _ := body["messages"].([]any)
				if len(messages) != len(history) {
					t.Fatalf("sent %d messages, want %d", len(messages), len(history))
				}
				for i, element := range messages {
					message := element.(map[string]any)
					if _, ok := message["origin"]; ok {
						t.Fatalf("origin leaked upstream in message %d", i)
					}
					if _, ok := message["continuation"]; ok {
						t.Fatalf("continuation leaked upstream in message %d", i)
					}
				}
				if history[1].Continuation == nil || !reflect.DeepEqual(history, expected) {
					t.Fatal("stored history changed")
				}
			})
		}
	}
}
