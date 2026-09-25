package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/internal/sse"
)

func TestChatRoundTerminalMatrix(t *testing.T) {
	call := `{"index":0,"id":"upstream-a","type":"function","function":{"name":"read","arguments":"{}"}}`
	call2 := `{"index":1,"id":"upstream-b","type":"function","function":{"name":"read","arguments":"["}}`
	tool := func(c string) string { return `{"choices":[{"index":0,"delta":{"tool_calls":[` + c + `]}}]}` }
	finish := func(reason string) string {
		return `{"choices":[{"index":0,"delta":{},"finish_reason":"` + reason + `"}]}`
	}
	usage := `{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
	tests := []struct {
		name       string
		chunks     []string
		done       bool
		kind       string
		executions int
	}{
		{"stop", []string{`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`, finish("stop")}, true, "", 0},
		{"tool_calls", []string{tool(call), finish("tool_calls")}, true, "", 1},
		{"usage_after_finish", []string{tool(call), finish("tool_calls"), usage}, true, "", 1},
		{"length_with_call", []string{tool(call), finish("length")}, true, "incomplete", 0},
		{"filter_with_call", []string{tool(call), finish("content_filter")}, true, "incomplete", 0},
		{"stop_with_call", []string{tool(call), finish("stop")}, true, "protocol", 0},
		{"calls_missing", []string{finish("tool_calls")}, true, "protocol", 0},
		{"done_without_finish", []string{tool(call)}, true, "protocol", 0},
		{"finish_without_done", []string{tool(call), finish("tool_calls")}, false, "truncated", 0},
		{"conflicting_finishes", []string{tool(call), finish("tool_calls"), finish("stop")}, true, "protocol", 0},
		{"delta_after_finish", []string{tool(call), finish("tool_calls"), `{"choices":[{"index":0,"delta":{"content":"late"}}]}`}, true, "protocol", 0},
		{"unknown_finish", []string{finish("mystery")}, true, "protocol", 0},
		{"deprecated_call", []string{`{"choices":[{"index":0,"delta":{"function_call":{"name":"read","arguments":"{}"}}}]}`, finish("stop")}, true, "protocol", 0},
		{"multiple_choices", []string{`{"choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}`, finish("stop")}, true, "protocol", 0},
		{"missing_index", []string{`{"choices":[{"index":0,"delta":{"tool_calls":[{"type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`, finish("tool_calls")}, true, "protocol", 0},
		{"malformed_arguments", []string{tool(call2), finish("tool_calls")}, true, "protocol", 0},
		{"one_bad_of_two", []string{tool(call + "," + call2), finish("tool_calls")}, true, "protocol", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				w.Header().Set("Content-Type", "text/event-stream")
				if requests > 1 {
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"}}]}\n\ndata: "+finish("stop")+"\n\ndata: [DONE]\n\n")
					return
				}
				for _, chunk := range tt.chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
				}
				if tt.done {
					fmt.Fprint(w, "data: [DONE]\n\n")
				}
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			writer, err := sse.NewWriter(recorder)
			if err != nil {
				t.Fatal(err)
			}
			executor := &recordingExecutor{result: "read result"}
			err = runTestToolLoop(context.Background(), NewClient(server.URL, "test", "", true),
				[]Message{{Role: "user", Content: StringContent("read")}}, "test", 0,
				[]Tool{{Type: "function", Function: &FunctionDef{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}},
				executor, writer, nil)
			if tt.kind == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.kind != "" && (err == nil || !strings.HasPrefix(err.Error(), tt.kind+":")) {
				t.Fatalf("want '%s' error, got %v", tt.kind, err)
			}
			if got := executor.callCount(); got != tt.executions {
				t.Fatalf("executor calls: got %d, want %d", got, tt.executions)
			}
		})
	}
}

func TestChatRoundToolPreviewNameTiming(t *testing.T) {
	callDelta := func(name, arguments string, initial bool) string {
		call := map[string]any{
			"index":    0,
			"function": map[string]any{"name": name, "arguments": arguments},
		}
		if initial {
			call["id"] = "upstream-a"
			call["type"] = "function"
		}
		chunk := map[string]any{"choices": []any{map[string]any{
			"index": 0, "delta": map[string]any{"tool_calls": []any{call}},
		}}}
		data, err := json.Marshal(chunk)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, tt := range []struct {
		name         string
		chunks       []string
		wantKinds    []string
		previewNames []string
	}{
		{
			name: "name in first chunk",
			chunks: []string{
				callDelta("read", `{"path":`, true),
				callDelta("", `"README.md"}`, false),
			},
			wantKinds:    []string{"tool_call_start", "tool_call_args", "tool_call_args"},
			previewNames: []string{"read"},
		},
		{
			name: "name after arguments",
			chunks: []string{
				callDelta("", `{"path":`, true),
				callDelta("", `"README.md"`, false),
				callDelta("read", `}`, false),
			},
			wantKinds:    []string{"tool_call_start", "tool_call_args", "tool_call_args", "tool_call_start", "tool_call_args"},
			previewNames: []string{"", "read"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range tt.chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()

			var events []RoundEvent
			final, err := NewClient(server.URL, "test", "", false).Round(context.Background(),
				RoundRequest{Model: "test", Intent: IntentTools}, func(event RoundEvent) {
					events = append(events, event)
				})
			if err != nil {
				t.Fatal(err)
			}
			if len(final.Calls) != 1 || final.Calls[0].Name != "read" || final.Calls[0].Arguments != `{"path":"README.md"}` {
				t.Fatalf("final call = %#v", final.Calls)
			}
			if len(events) != len(tt.wantKinds) {
				t.Fatalf("events = %#v", events)
			}
			preview := 0
			for i, event := range events {
				if event.Kind != tt.wantKinds[i] || event.Call.ID != final.Calls[0].ID || event.Call.Index != 0 {
					t.Fatalf("event %d = %#v", i, event)
				}
				if event.Kind == "tool_call_start" {
					if event.Call.Name != tt.previewNames[preview] {
						t.Fatalf("preview %d name = %q, want %q", preview, event.Call.Name, tt.previewNames[preview])
					}
					preview++
				}
			}
			if preview != len(tt.previewNames) {
				t.Fatalf("preview count = %d", preview)
			}
		})
	}
}
