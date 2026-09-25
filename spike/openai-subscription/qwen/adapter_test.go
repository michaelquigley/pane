package qwen

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

func adapter(t *testing.T, profile, effort, endpoint string) *Adapter {
	t.Helper()
	if endpoint == "" {
		endpoint = "http://qwen.invalid:11400/v1"
	}
	a, err := New(Config{Alias: "qwen3.8-27b@test", Endpoint: endpoint, APIKey: "QWEN-KEY", UpstreamModel: "qwen3.8-27b", Profile: profile, Effort: effort, MaxTokens: 24756}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func body(t *testing.T, a *Adapter, req round.Request) map[string]any {
	t.Helper()
	b, err := a.BuildBody(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

var addTool = []round.Tool{{Name: "add", Description: "add", Parameters: json.RawMessage(`{"type":"object"}`)}}

func TestProfileControls(t *testing.T) {
	h := []round.Message{{Role: "user", Content: round.Str("hi")}}

	n := body(t, adapter(t, "qwen3.8-ninfer", "low", ""), round.Request{History: h})
	if n["reasoning_effort"] != "low" || n["chat_template_kwargs"] != nil || n["temperature"] != nil || n["max_tokens"] != float64(24756) {
		t.Fatalf("ninfer low: %v", n)
	}
	n = body(t, adapter(t, "qwen3.8-ninfer", "none", ""), round.Request{History: h})
	if n["reasoning_effort"] != "none" || n["temperature"] != nil {
		t.Fatalf("ninfer none must leave sampling to the engine: %v", n)
	}

	l := body(t, adapter(t, "qwen3.8-llamacpp", "none", ""), round.Request{History: h})
	if l["reasoning_effort"] != "none" || l["temperature"] != 0.7 || l["top_p"] != 0.8 || l["top_k"] != float64(20) {
		t.Fatalf("llamacpp none: %v", l)
	}
	if kw := l["chat_template_kwargs"].(map[string]any); kw["preserve_reasoning"] != false || len(kw) != 1 {
		t.Fatalf("llamacpp kwargs: %v", kw)
	}
	l = body(t, adapter(t, "qwen3.8-llamacpp", "medium", ""), round.Request{History: h})
	if l["temperature"] != nil {
		t.Fatal("thinking-mode request must not override sampling")
	}

	omitted := body(t, adapter(t, "qwen3.8-ninfer", "", ""), round.Request{History: h})
	if _, ok := omitted["reasoning_effort"]; ok {
		t.Fatal("omitted effort must not be sent")
	}
	for _, b := range []map[string]any{n, l, omitted} {
		raw, _ := json.Marshal(b)
		if strings.Contains(string(raw), "enable_thinking") {
			t.Fatal("enable_thinking must never be sent alongside reasoning_effort")
		}
	}
}

func TestProfileValidation(t *testing.T) {
	bad := []Config{
		{Profile: "qwen3.8-llamacpp", Effort: "high", MaxTokens: 1},
		{Profile: "qwen3.8-ninfer", Effort: "minimal", MaxTokens: 1},
		{Profile: "", Effort: "low", MaxTokens: 1},
		{Profile: "qwen3.8-vllm", MaxTokens: 1},
		{Profile: "qwen3.8-ninfer", Effort: "low"}, // no explicit max_tokens
	}
	for _, c := range bad {
		c.Endpoint = "http://x:1/v1"
		if _, err := New(c, nil); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestActiveRoundReasoningReplayOnly(t *testing.T) {
	a := adapter(t, "qwen3.8-llamacpp", "", "")
	self := &round.Origin{Alias: "q", Identity: a.Identity()}
	foreign := &round.Origin{Alias: "sol", Identity: round.Identity{Provider: "openai-codex", Protocol: "responses", UpstreamModel: "gpt-5.6-sol", Service: "chatgpt.com/backend-api/codex"}}
	h := []round.Message{
		{Role: "system", Content: round.Str("sys")},
		{Role: "user", Content: round.Str("q1")},
		{Role: "assistant", Content: round.Str("a1"), Origin: self},
		{Role: "user", Content: round.Str("q2")},
		{Role: "assistant", Origin: self, ToolCalls: []round.ToolCall{{ID: "c1", Name: "add", Arguments: `{"a":1,"b":2}`}}},
		{Role: "tool", ToolCallID: "c1", Content: round.Str("3")},
		{Role: "assistant", Origin: foreign, ToolCalls: []round.ToolCall{{ID: "c2", Name: "add", Arguments: `{"a":3,"b":3}`}}},
		{Role: "tool", ToolCallID: "c2", Content: round.Str("6")},
	}
	local := map[int]round.Local{2: {Reasoning: "CLOSED"}, 4: {Reasoning: "ACTIVE"}, 6: {Reasoning: "FOREIGN"}}
	b := body(t, a, round.Request{History: h, Tools: addTool, Local: local})
	msgs := b["messages"].([]any)
	get := func(i int) string {
		v, _ := msgs[i].(map[string]any)["reasoning_content"].(string)
		return v
	}
	if get(2) != "" || get(4) != "ACTIVE" || get(6) != "" {
		t.Fatalf("replay: closed=%q active=%q foreign=%q", get(2), get(4), get(6))
	}
	// the stored history carries no reasoning at all.
	raw, _ := json.Marshal(h)
	if strings.Contains(string(raw), "ACTIVE") {
		t.Fatal("request-local reasoning leaked into stored history")
	}
}

func TestForcedFinalPlacement(t *testing.T) {
	h := []round.Message{
		{Role: "system", Content: round.Str("sys")},
		{Role: "user", Content: round.Str("q")},
		{Role: "assistant", ToolCalls: []round.ToolCall{{ID: "c1", Name: "add", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "c1", Content: round.Str("error")},
	}
	for _, p := range []string{"qwen3.8-ninfer", "qwen3.8-llamacpp"} {
		b := body(t, adapter(t, p, "", ""), round.Request{History: h, Tools: addTool, Intent: round.IntentForcedFinal})
		msgs := b["messages"].([]any)
		first := msgs[0].(map[string]any)
		last := msgs[len(msgs)-1].(map[string]any)
		if b["tools"] != nil || len(msgs) != 4 || first["role"] != "system" || !strings.HasSuffix(first["content"].(string), round.ForcedFinalInstruction) || last["role"] != "tool" {
			t.Fatalf("%s forced final: %v", p, b)
		}
		if h[0].Content == nil || *h[0].Content != "sys" {
			t.Fatal("stored system message rewritten")
		}
	}
	// no system message at all: one is inserted at the front.
	b := body(t, adapter(t, "qwen3.8-ninfer", "", ""), round.Request{History: h[1:], Intent: round.IntentForcedFinal})
	if b["messages"].([]any)[0].(map[string]any)["role"] != "system" {
		t.Fatal("instruction not placed first")
	}
	// the generic connection keeps pane's current trailing system message.
	g, _ := New(Config{Endpoint: "http://x:1/v1", UpstreamModel: "m"}, nil)
	gb := body(t, g, round.Request{History: h, Intent: round.IntentForcedFinal})
	msgs := gb["messages"].([]any)
	if msgs[len(msgs)-1].(map[string]any)["role"] != "system" {
		t.Fatal("generic forced-final placement changed")
	}
}

func serve(t *testing.T, payload string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer QWEN-KEY" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/v1"
}

const toolStream = `data: {"choices":[{"delta":{"role":"assistant"}}]}

data: {"choices":[{"delta":{"reasoning_content":"need add"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"x","type":"function","function":{"name":"add","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":2,"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"b\":2}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":12,"total_tokens":62}}

data: [DONE]

`

func TestStreamParsing(t *testing.T) {
	a := adapter(t, "qwen3.8-llamacpp", "low", serve(t, toolStream))
	var thinking string
	f, err := a.Round(context.Background(), round.Request{History: []round.Message{{Role: "user", Content: round.Str("2+2")}}, Tools: addTool}, func(e round.Event) {
		if e.Kind == round.EventThinking {
			thinking += e.Delta
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Executable() || f.Assistant.ToolCalls[0].Arguments != `{"a":2,"b":2}` || f.Local.Reasoning != "need add" || thinking != "need add" {
		t.Fatalf("final %+v local %+v", f, f.Local)
	}
	if f.Usage.TotalTokens != 62 || f.RawFinish != "tool_calls" {
		t.Fatalf("usage/finish %+v %s", f.Usage, f.RawFinish)
	}
	if f.Assistant.Origin == nil || f.Assistant.Origin.Identity.Profile != "qwen3.8-llamacpp" || f.Assistant.Origin.Effort != "low" || f.Assistant.Continuation != nil {
		t.Fatal("origin/continuation")
	}
}

func TestStreamFaults(t *testing.T) {
	lines := strings.SplitAfter(toolStream, "\n\n")
	cases := map[string]struct {
		payload string
		kind    round.ErrorKind
	}{
		"eof before done":   {strings.Join(lines[:5], ""), round.ErrTruncated},
		"done no finish":    {strings.Join(lines[:5], "") + "data: [DONE]\n\n", round.ErrTruncated},
		"length with calls": {strings.Join(lines[:5], "") + `data: {"choices":[{"delta":{},"finish_reason":"length"}]}` + "\n\ndata: [DONE]\n\n", round.ErrIncomplete},
		"malformed args":    {strings.Join(lines[:4], "") + `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n", round.ErrIncomplete},
		"bad chunk":         {"data: {x\n\n", round.ErrProtocol},
	}
	for name, c := range cases {
		a := adapter(t, "qwen3.8-ninfer", "", serve(t, c.payload))
		f, err := a.Round(context.Background(), round.Request{History: []round.Message{{Role: "user", Content: round.Str("x")}}, Tools: addTool}, nil)
		var re *round.Error
		if f != nil || !errors.As(err, &re) || re.Kind != c.kind {
			t.Fatalf("%s: final=%v err=%v", name, f, err)
		}
	}
}
