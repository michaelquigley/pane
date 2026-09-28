package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/config"
)

type qwenSink func(LoopEvent) error

func (f qwenSink) Emit(event LoopEvent) error { return f(event) }

type qwenExecutor struct{}

func (qwenExecutor) NeedsApproval(string) bool { return false }
func (qwenExecutor) CallTool(context.Context, string, map[string]any) ToolExecution {
	return ToolExecution{Dispatch: ResultReceived, Content: "4"}
}

func qwenToolStream() string {
	return "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"ACTIVE_CANARY\",\"tool_calls\":[{\"index\":0,\"id\":\"call_upstream\",\"type\":\"function\",\"function\":{\"name\":\"add\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
}
func qwenTextStream() string {
	return "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"4\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
}

func TestQwenActiveReasoningOnlyNextToolRound(t *testing.T) {
	for _, profile := range []string{config.ProfileNinfer, config.ProfileLlamaCPP} {
		t.Run(profile, func(t *testing.T) {
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				bodies = append(bodies, body)
				w.Header().Set("Content-Type", "text/event-stream")
				if len(bodies) == 1 {
					_, _ = io.WriteString(w, qwenToolStream())
				} else {
					_, _ = io.WriteString(w, qwenTextStream())
				}
			}))
			defer server.Close()
			client, err := NewQwenClient(server.URL, "qwen3.8-27b", "", profile, "low", false)
			if err != nil {
				t.Fatal(err)
			}
			var roundEvents []LoopEvent
			err = RunToolLoop(context.Background(), client, Turn{}, []Message{{Role: "system", Content: StringContent("base")}, {Role: "user", Content: StringContent("add")}}, "qwen3.8-27b", 4096, []Tool{{Type: "function", Function: &FunctionDef{Name: "add", Parameters: json.RawMessage(`{"type":"object"}`)}}}, qwenExecutor{}, qwenSink(func(event LoopEvent) error { roundEvents = append(roundEvents, event); return nil }), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 2 {
				t.Fatalf("got %d requests", len(bodies))
			}
			messages := bodies[1]["messages"].([]any)
			assistant := messages[2].(map[string]any)
			if assistant["reasoning_content"] != "ACTIVE_CANARY" {
				t.Fatalf("active reasoning absent: %#v", assistant)
			}
			if _, ok := assistant["origin"]; ok {
				t.Fatal("origin leaked upstream")
			}
			if _, ok := assistant["continuation"]; ok {
				t.Fatal("continuation leaked upstream")
			}
			for _, event := range roundEvents {
				if event.Kind == LoopRoundComplete {
					if event.Round.Assistant.Continuation != nil {
						t.Fatal("qwen acquired durable continuation")
					}
					unbound, err := dd.Unbind(event.Round.Assistant)
					if err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(unbound)
					if strings.Contains(string(encoded), "ACTIVE_CANARY") || strings.Contains(string(encoded), "reasoning_content") {
						t.Fatalf("reasoning leaked into round event: %s", encoded)
					}
				}
			}
			if profile == config.ProfileNinfer {
				if bodies[0]["preserve_thinking"] != false || bodies[0]["chat_template_kwargs"] != nil {
					t.Fatalf("wrong ninfer preservation: %#v", bodies[0])
				}
			} else {
				kw := bodies[0]["chat_template_kwargs"].(map[string]any)
				if kw["preserve_reasoning"] != false || bodies[0]["preserve_thinking"] != nil {
					t.Fatalf("wrong llama.cpp preservation: %#v", bodies[0])
				}
			}
			if bodies[0]["reasoning_effort"] != "low" || bodies[1]["reasoning_effort"] != "low" {
				t.Fatal("effort changed between tool rounds")
			}
		})
	}
}

func TestQwenDisabledThinkingAndForcedFinal(t *testing.T) {
	for _, profile := range []string{config.ProfileNinfer, config.ProfileLlamaCPP} {
		t.Run(profile, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, qwenTextStream())
			}))
			defer server.Close()
			client, err := NewQwenClient(server.URL, "qwen3.8-27b", "", profile, "none", false)
			if err != nil {
				t.Fatal(err)
			}
			messages := []Message{{Role: "system", Content: StringContent("base")}, {Role: "user", Content: StringContent("add")}, {Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Type: "function", Function: ToolCallFunction{Name: "add", Arguments: "{}"}}}}, {Role: "tool", ToolCallID: "c1", Content: StringContent("4")}, {Role: "system", Content: StringContent(forceFinalAfterToolFailureText)}}
			_, err = client.Round(context.Background(), RoundRequest{Model: "qwen3.8-27b", Messages: messages, Intent: IntentFinal}, func(RoundEvent) {})
			if err != nil {
				t.Fatal(err)
			}
			wire := body["messages"].([]any)
			if len(wire) != 4 || wire[0].(map[string]any)["role"] != "system" || !strings.Contains(wire[0].(map[string]any)["content"].(string), forceFinalAfterToolFailureText) || wire[3].(map[string]any)["role"] != "tool" {
				t.Fatalf("forced-final history moved incorrectly: %#v", wire)
			}
			if _, ok := body["tools"]; ok {
				t.Fatal("tools offered on forced final")
			}
			if body["reasoning_effort"] != "none" {
				t.Fatal("thinking disable missing")
			}
			if profile == config.ProfileNinfer {
				if _, ok := body["temperature"]; ok {
					t.Fatal("ninfer sampling override added")
				}
			} else if body["temperature"] != 0.7 || body["top_p"] != 0.8 || body["top_k"] != float64(20) {
				t.Fatalf("wrong llama.cpp nonthinking preset: %#v", body)
			}
		})
	}
}
