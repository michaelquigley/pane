package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/michaelquigley/pane/internal/auth"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/mcp"
)

// fakeSubscription is a synthetic shared credential manager: no files, no
// login, no refresh, no provider traffic.
type fakeSubscription struct {
	mu        sync.Mutex
	account   string
	token     string
	expiry    string
	statusErr error
	accesses  int
	statuses  int
}

func (s *fakeSubscription) CurrentAccount(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.account, nil
}

func (s *fakeSubscription) AccessForAccount(_ context.Context, account string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accesses++
	if account != s.account {
		return "", errors.New("subscription account changed during request")
	}
	return s.token, nil
}

func (s *fakeSubscription) Status() (auth.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses++
	if s.statusErr != nil {
		return auth.Status{}, s.statusErr
	}
	if s.account == "" {
		return auth.Status{}, nil
	}
	return auth.Status{SignedIn: true, ExpiryMarker: s.expiry}, nil
}

func (s *fakeSubscription) set(account, token, expiry string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.account, s.token, s.expiry = account, token, expiry
}

// scriptedProvider records every request body and answers the n-th request
// with the n-th scripted stream, or the last one when the script runs out.
type scriptedProvider struct {
	mu      sync.Mutex
	bodies  []string
	headers []http.Header
	script  []string
	status  int
}

func (p *scriptedProvider) next(r *http.Request) (int, string) {
	body, _ := io.ReadAll(r.Body)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies = append(p.bodies, string(body))
	p.headers = append(p.headers, r.Header.Clone())
	status := p.status
	if status == 0 {
		status = http.StatusOK
	}
	if len(p.script) == 0 {
		return status, ""
	}
	index := len(p.bodies) - 1
	if index >= len(p.script) {
		index = len(p.script) - 1
	}
	return status, p.script[index]
}

func (p *scriptedProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	status, stream := p.next(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, stream)
}

// RoundTrip serves the subscription route in-process, so no request leaves
// the test.
func (p *scriptedProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	status, stream := p.next(r)
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(stream)), Header: make(http.Header), Request: r}, nil
}

func (p *scriptedProvider) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.bodies...)
}

// countingTools is a harmless tool source that records every execution.
type countingTools struct {
	mu    sync.Mutex
	calls []string
}

func (c *countingTools) GetAllModelTools() []llm.Tool {
	return []llm.Tool{
		{Type: "function", Function: &llm.FunctionDef{Name: "add", Parameters: json.RawMessage(`{"type":"object"}`)}},
		{Type: "function", Function: &llm.FunctionDef{Name: "write_note", Parameters: json.RawMessage(`{"type":"object"}`)}},
	}
}

func (c *countingTools) NeedsApproval(string) bool { return false }

func (c *countingTools) CallTool(_ context.Context, name string, _ map[string]any) llm.ToolExecution {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, name)
	return llm.ToolExecution{Dispatch: llm.ResultReceived, Content: "42"}
}

func (c *countingTools) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

type turnHarness struct {
	api          *API
	chat         *scriptedProvider
	codex        *scriptedProvider
	subscription *fakeSubscription
	tools        *countingTools
}

// newTurnHarness builds an API over a registry with a generic alias, a qwen
// profile alias, and two subscription aliases with different effort
// presets. its session store is nil: the chat path must never touch it.
func newTurnHarness(t *testing.T) *turnHarness {
	t.Helper()
	chat := &scriptedProvider{script: []string{chatText("ok")}}
	server := httptest.NewServer(chat)
	t.Cleanup(server.Close)
	codexProvider := &scriptedProvider{script: []string{codexText("ok")}}
	codexName, qwenProfile := config.ProviderCodex, config.ProfileNinfer
	low, high, medium := "low", "high", "medium"
	cfg := &config.Config{Endpoint: server.URL, Model: "qwen", Listen: "127.0.0.1:8400", Models: map[string]*config.ModelConfig{
		"qwen":        {UpstreamModel: "qwen3.8-27b"},
		"qwen@ninfer": {UpstreamModel: "qwen3.8-27b", CompatibilityProfile: qwenProfile, ReasoningEffort: &medium},
		"sol":         {Provider: &codexName, UpstreamModel: "gpt-5.6-sol", ReasoningEffort: &low},
		"sol@high":    {Provider: &codexName, UpstreamModel: "gpt-5.6-sol", ReasoningEffort: &high},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	a := NewAPI(cfg, llm.NewClient(cfg.Endpoint, cfg.Model, "", false), mustModelClients(t, cfg), mcp.NewManager(nil), nil)
	tools := &countingTools{}
	a.tools = tools
	subscription := &fakeSubscription{account: "acct-pane-test-a", token: "token-a", expiry: "1"}
	a.SetSubscriptionAuth(subscription, nil)
	a.codexTransport = codexProvider
	return &turnHarness{api: a, chat: chat, codex: codexProvider, subscription: subscription, tools: tools}
}

func chatText(text string) string {
	content, _ := json.Marshal(text)
	return "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":" + string(content) + "},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
}

func chatToolCall(name, arguments string) string {
	args, _ := json.Marshal(arguments)
	return "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"up_1\",\"type\":\"function\",\"function\":{\"name\":\"" + name + "\",\"arguments\":" + string(args) + "}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
}

func codexText(text string) string {
	item, _ := json.Marshal(map[string]any{"type": "message", "id": "msg_SYNTH", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
	terminal, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []json.RawMessage{item}}})
	return "data: " + string(terminal) + "\n\n"
}

func codexToolCall(name, arguments string) string {
	item, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_SYNTH", "status": "completed", "call_id": "call_SYNTH", "name": name, "arguments": arguments})
	terminal, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []json.RawMessage{item}}})
	return "data: " + string(terminal) + "\n\n"
}

type recordedEvent struct {
	Type string
	Data map[string]any
	Raw  string
}

func (h *turnHarness) post(t *testing.T, body string) (*httptest.ResponseRecorder, []recordedEvent) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
	recorder := httptest.NewRecorder()
	h.api.handleChat(recorder, request)
	return recorder, parseTurnEvents(t, recorder.Body.String())
}

func parseTurnEvents(t *testing.T, stream string) []recordedEvent {
	t.Helper()
	var events []recordedEvent
	var current recordedEvent
	scanner := bufio.NewScanner(strings.NewReader(stream))
	scanner.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current.Type = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			current.Raw = strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(current.Raw), &current.Data); err != nil {
				t.Fatalf("malformed event data %q: %v", current.Raw, err)
			}
		case line == "" && current.Type != "":
			events = append(events, current)
			current = recordedEvent{}
		}
	}
	return events
}

func eventTypes(events []recordedEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

// chatErrorCode decodes a typed pre-stream refusal.
func chatErrorCode(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not a typed error (status %d): %q", recorder.Code, recorder.Body.String())
	}
	return body.Error.Code
}

// freshTurn is the minimal new-contract request body for one user message.
func freshTurn(model, text string) string {
	content, _ := json.Marshal(text)
	return `{"model":"` + model + `","turn_id":"t1","recovery":{"v":1,"turns":[]},"messages":[{"role":"user","content":` + string(content) + `,"turn_id":"t1"}]}`
}
