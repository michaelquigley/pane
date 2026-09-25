package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatRequestDdProjectionPreservesNullContentAndToolSchema(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL, "test", "", false)
	stream, err := client.StreamChat(context.Background(), &ChatRequest{
		Model: "test",
		Messages: []Message{{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "pane-call", Type: "function", Function: ToolCallFunction{Name: "read", Arguments: `{}`},
		}}}},
		Tools: []Tool{{Type: "function", Function: &FunctionDef{
			Name: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":2}}}`),
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decoding request: %v", err)
	}
	message := request["messages"].([]any)[0].(map[string]any)
	if content, present := message["content"]; !present || content != nil {
		t.Fatalf("assistant content should be explicit null: %#v", message)
	}
	parameters := request["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"]
	if parameters.(map[string]any)["type"] != "object" {
		t.Fatalf("tool schema was not an object: %#v", parameters)
	}
	path := parameters.(map[string]any)["properties"].(map[string]any)["path"].(map[string]any)
	if path["minLength"] != float64(2) {
		t.Fatalf("numeric schema constraint changed: %#v", path)
	}
	if request["stream"] != true {
		t.Fatalf("stream flag absent: %#v", request)
	}
	if _, present := request["max_tokens"]; present {
		t.Fatalf("unset token cap was emitted: %#v", request)
	}
	if _, present := request["stream_options"]; present {
		t.Fatalf("disabled usage options were emitted: %#v", request)
	}
}
