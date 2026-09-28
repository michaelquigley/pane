package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/auth"
	"github.com/michaelquigley/pane/internal/llm"
)

type fakeCredentials struct {
	mu             sync.Mutex
	account, token string
	accesses       int
}

func (c *fakeCredentials) CurrentAccount(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.account, nil
}
func (c *fakeCredentials) AccessForAccount(_ context.Context, account string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accesses++
	if c.account != account {
		return "", errors.New("account changed")
	}
	return c.token, nil
}
func (c *fakeCredentials) change(account, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.account, c.token = account, token
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func sseResponse(data string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header)}
}
func responseText(text string) string {
	item, _ := json.Marshal(map[string]any{"type": "message", "id": "msg_SYNTH", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
	terminal, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": []json.RawMessage{item}}})
	return "data: " + string(terminal) + "\n\n"
}
func toolFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../../spike/openai-subscription/testdata/codex/toolcall.sse")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func newAdapter(t *testing.T, creds *fakeCredentials, transport http.RoundTripper) *Adapter {
	t.Helper()
	a, err := New(context.Background(), "sol", "gpt-5.6-sol", "medium", creds, transport)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func baseRequest() llm.RoundRequest {
	return llm.RoundRequest{Model: "gpt-5.6-sol", Intent: llm.IntentTools, Messages: []llm.Message{{Role: "user", Content: llm.StringContent("add 2 and 2")}}, Tools: []llm.Tool{{Type: "function", Function: &llm.FunctionDef{Name: "add", Parameters: json.RawMessage(`{"type":"object"}`)}}}}
}

func TestToolFixtureContinuationAndReplay(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "token-A"}
	var bodies []map[string]any
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != auth.GenerationURL || req.Header.Get("ChatGPT-Account-ID") != "acct-pane-test-a" || req.Header.Get("Authorization") != "Bearer token-A" {
			t.Fatal("wrong generation destination or credential binding")
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			return sseResponse(toolFixture(t)), nil
		}
		return sseResponse(responseText("4")), nil
	})
	a := newAdapter(t, creds, transport)
	req := baseRequest()
	final, err := a.Round(context.Background(), req, func(llm.RoundEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if final.Finish != "tool_calls" || len(final.Calls) != 1 || final.Continuation == nil {
		t.Fatalf("bad final: %+v", final)
	}
	if len(final.Continuation.Items) != 2 || final.Continuation.Bindings[0].ProviderCallID != "call_SYNTH_2" || final.Continuation.Bindings[0].ProviderItemID != "fc_SYNTH_2" {
		t.Fatalf("lost output item order or ids: %+v", final.Continuation)
	}
	if !strings.Contains(string(final.Continuation.Items[0]), "ENC_SYNTH_2") {
		t.Fatal("encrypted reasoning with empty summary was lost")
	}
	assistant := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: final.Calls[0].ID, Type: "function", Function: llm.ToolCallFunction{Name: final.Calls[0].Name, Arguments: final.Calls[0].Arguments}}}, Origin: final.Origin, Continuation: final.Continuation}
	unbound, err := dd.Unbind(assistant)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := json.Marshal(unbound)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(serialized, &saved); err != nil {
		t.Fatal(err)
	}
	continuation := saved["continuation"].(map[string]any)
	if _, ok := continuation["items"].([]any)[0].(map[string]any); !ok {
		t.Fatalf("continuation item encoded incorrectly: %s", serialized)
	}
	var restored llm.Message
	if err := dd.BindJSON(&restored, serialized); err != nil {
		t.Fatalf("continuation failed to round-trip through chat binding: %v", err)
	}
	if restored.Continuation == nil || len(restored.Continuation.Items) != 2 {
		t.Fatal("continuation not restored")
	}
	req.Messages = append(req.Messages, assistant, llm.Message{Role: "tool", ToolCallID: final.Calls[0].ID, Content: llm.StringContent("4")})
	_, err = a.Round(context.Background(), req, func(llm.RoundEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	input := bodies[1]["input"].([]any)
	if input[1].(map[string]any)["id"] != "rs_SYNTH_2" || input[2].(map[string]any)["id"] != "fc_SYNTH_2" || input[3].(map[string]any)["call_id"] != "call_SYNTH_2" {
		t.Fatalf("compatible replay changed ids/order: %#v", input)
	}
	if bodies[1]["store"] != false || bodies[1]["reasoning"].(map[string]any)["effort"] != "medium" || bodies[1]["include"].([]any)[0] != "reasoning.encrypted_content" {
		t.Fatalf("wrong request controls: %#v", bodies[1])
	}
	if _, ok := bodies[1]["previous_response_id"]; ok {
		t.Fatal("stateful response dependency")
	}
	if _, ok := input[1].(map[string]any)["safety_identifier"]; ok {
		t.Fatal("response object leaked into continuation")
	}
}

func TestEnvelopeExclusionAndPortableCallIDs(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil }))
	for _, id := range []string{"a.b", "a/b", strings.Repeat("a", 80)} {
		if portableCallID(id) == portableCallID("a_b") {
			t.Fatalf("portable call id collision for %q", id)
		}
	}
	request := baseRequest()
	request.Messages = append(request.Messages, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "a.b", Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: "{}"}}}, Continuation: &llm.Continuation{Format: "unknown", Version: 99}}, llm.Message{Role: "tool", ToolCallID: "a.b", Content: llm.StringContent("4")})
	body, err := a.buildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Input[1]["call_id"] != portableCallID("a.b") || parsed.Input[2]["call_id"] != portableCallID("a.b") {
		t.Fatal("portable pair not preserved")
	}
	request.Messages[1].ToolCalls[0].ID = "orphan"
	if _, err := a.buildRequest(request); err == nil {
		t.Fatal("orphaned result accepted")
	}
}

func TestReplayCompatibilityAcrossAliasModelAndAccount(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected request"); return nil, nil }))
	origin := &llm.RoundOrigin{Alias: "old-alias", Identity: a.identity}
	itemReasoning := json.RawMessage(`{"type":"reasoning","id":"rs_A","status":"completed","summary":[],"encrypted_content":"ENC_A"}`)
	itemCall := json.RawMessage(`{"type":"function_call","id":"fc_A","status":"completed","call_id":"call_A","name":"add","arguments":"{}"}`)
	call := llm.ToolCall{ID: "pane.a", Type: "function", Function: llm.ToolCallFunction{Name: "add", Arguments: "{}"}}
	message := llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{call}, Origin: origin, Continuation: &llm.Continuation{Format: continuationFormat, Version: 1, Identity: a.identity, Items: []json.RawMessage{itemReasoning, itemCall}, Bindings: []llm.CallBinding{{PaneCallID: call.ID, ProviderCallID: "call_A", ProviderItemID: "fc_A"}}}}
	request := baseRequest()
	request.Messages = append(request.Messages, message, llm.Message{Role: "tool", ToolCallID: call.ID, Content: llm.StringContent("4")})
	inspect := func(adapter *Adapter, replay bool) {
		body, err := adapter.buildRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatal(err)
		}
		got := wire.Input[1]["id"] == "rs_A"
		if got != replay {
			t.Fatalf("replay=%v, want %v: %s", got, replay, body)
		}
		if replay && (wire.Input[2]["id"] != "fc_A" || wire.Input[3]["call_id"] != "call_A") {
			t.Fatal("compatible ids or output association changed")
		}
		if !replay && wire.Input[len(wire.Input)-1]["call_id"] != portableCallID(call.ID) {
			t.Fatal("portable output binding missing")
		}
	}
	inspect(a, true)
	message.Continuation.Version = 99
	request.Messages[1] = message
	inspect(a, false)
	message.Continuation.Version = 1
	request.Messages[1] = message
	creds.change("acct-pane-test-b", "B")
	b, err := New(context.Background(), "renamed", "gpt-5.6-sol", "medium", creds, nil)
	if err != nil {
		t.Fatal(err)
	}
	inspect(b, false)
	model, err := New(context.Background(), "astra", "gpt-6-astra", "medium", creds, nil)
	if err != nil {
		t.Fatal(err)
	}
	inspect(model, false)
}

func TestTerminalBackfillAndToolsWithheld(t *testing.T) {
	identity := llm.RoundIdentity{Provider: "openai-codex", Protocol: "responses", UpstreamModel: "gpt-5.6-sol", Service: auth.GenerationURL, AccountScope: auth.AccountScope("acct-pane-test-a")}
	stream := "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs\"}}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs\",\"status\":\"completed\",\"summary\":[]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"id\":\"rs\",\"status\":\"completed\",\"summary\":[],\"encrypted_content\":\"ENC\"},{\"type\":\"message\",\"id\":\"msg\",\"status\":\"completed\",\"role\":\"assistant\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"cannot do that\"}]}]}}\n\n"
	final, err := parseStream(context.Background(), strings.NewReader(stream), func(llm.RoundEvent) {}, identity, "sol", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if final.Finish != "stop" || final.Content != "cannot do that" || final.Continuation == nil || !strings.Contains(string(final.Continuation.Items[0]), "ENC") {
		t.Fatalf("backfill/refusal lost: %+v", final)
	}
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	a := newAdapter(t, creds, nil)
	request := baseRequest()
	request.Intent = llm.IntentFinal
	request.Messages = append(request.Messages, llm.Message{Role: "system", Content: llm.StringContent("answer without tools")})
	body, err := a.buildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["tools"]; ok {
		t.Fatal("tools offered in forced final")
	}
	if !strings.Contains(wire["instructions"].(string), "answer without tools") {
		t.Fatal("forced final instruction omitted")
	}
	request.MaxTokens = 100
	if _, err := a.buildRequest(request); err == nil {
		t.Fatal("unsupported output cap accepted")
	}
}

func TestTerminalFailures(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	cases := []struct{ name, stream, kind string }{
		{"missing", "data: {\"type\":\"response.created\"}\n\n", "truncated"},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n", "incomplete"},
		{"contradictory", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"failed\",\"output\":[]}}\n\n", "protocol"},
		{"budget", "data: " + strings.Repeat(" ", maxRoundBytes) + "\n", "budget"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) { return sseResponse(tc.stream), nil }))
			_, err := a.Round(context.Background(), baseRequest(), func(llm.RoundEvent) {})
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != tc.kind {
				t.Fatalf("got %v, want %s", err, tc.kind)
			}
		})
	}
}

type loopSink func(llm.LoopEvent) error

func (f loopSink) Emit(event llm.LoopEvent) error { return f(event) }

type fakeExecutor struct{ calls int }

func (e *fakeExecutor) NeedsApproval(string) bool { return false }
func (e *fakeExecutor) CallTool(context.Context, string, map[string]any) llm.ToolExecution {
	e.calls++
	return llm.ToolExecution{Dispatch: llm.ResultReceived, Content: "4"}
}

func TestAccountChangeBetweenToolRounds(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		t.Run(map[bool]string{false: "switch", true: "rotate"}[rotate], func(t *testing.T) {
			creds := &fakeCredentials{account: "acct-pane-test-a", token: "A1"}
			requests := 0
			transport := transportFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Header.Get("ChatGPT-Account-ID") != "acct-pane-test-a" {
					t.Fatal("account header rebound")
				}
				if requests == 1 && req.Header.Get("Authorization") != "Bearer A1" || requests == 2 && req.Header.Get("Authorization") != "Bearer A2" {
					t.Fatal("wrong token for account")
				}
				if requests == 1 {
					return sseResponse(toolFixture(t)), nil
				}
				return sseResponse(responseText("4")), nil
			})
			a := newAdapter(t, creds, transport)
			executor := &fakeExecutor{}
			results := 0
			sink := loopSink(func(event llm.LoopEvent) error {
				if event.Kind == llm.LoopToolCallResult {
					results++
					if rotate {
						creds.change("acct-pane-test-a", "A2")
					} else {
						creds.change("acct-pane-test-b", "B")
					}
				}
				return nil
			})
			req := baseRequest()
			err := llm.RunToolLoop(context.Background(), a, llm.Turn{}, req.Messages, req.Model, 0, req.Tools, executor, sink, nil)
			if rotate && err != nil || !rotate && err == nil {
				t.Fatalf("unexpected loop result: %v", err)
			}
			wantRequests := 1
			if rotate {
				wantRequests = 2
			}
			if requests != wantRequests || executor.calls != 1 || results != 1 {
				t.Fatalf("requests=%d executions=%d results=%d", requests, executor.calls, results)
			}
		})
	}
}

// testIdentity is the synthetic v1 identity parseStream-only fixtures bind against.
func testIdentity() llm.RoundIdentity {
	return llm.RoundIdentity{Provider: "openai-codex", Protocol: "responses", UpstreamModel: "gpt-5.6-sol", Service: auth.GenerationURL, AccountScope: auth.AccountScope("acct-pane-test-a")}
}

func TestWireNullsUnknownFieldsAndExactNumbers(t *testing.T) {
	// shaped like the sanitized live capture: explicit nulls, provider
	// fields pane does not model, and token counts beyond float64's exact
	// range. the round must complete and retain verbatim item bytes.
	reasoningDone := `{"type":"reasoning","id":"rs_1","status":"completed","summary":[],"encrypted_content":"ENC\u00e9\u0000A"}`
	messageDone := `{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hi","annotations":[]}]}`
	stream := "data: {\"type\":\"response.created\",\"sequence_number\":1,\"response\":{\"status\":\"in_progress\",\"previous_response_id\":null,\"usage\":null,\"completed_at\":null,\"error\":null,\"access_programs\":null,\"output\":[]}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"sequence_number\":2,\"output_index\":0,\"item\":" + reasoningDone + "}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"sequence_number\":3,\"output_index\":0,\"item\":" + reasoningDone + "}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"sequence_number\":4,\"output_index\":1,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"role\":\"assistant\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"delta\":\"hi\",\"item_id\":\"msg_1\",\"obfuscation\":null}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"sequence_number\":5,\"output_index\":1,\"item\":" + messageDone + "}\n\n" +
		"data: {\"type\":\"response.completed\",\"sequence_number\":6,\"response\":{\"status\":\"completed\",\"error\":null,\"incomplete_details\":null,\"usage\":{\"input_tokens\":9007199254740993,\"output_tokens\":7,\"total_tokens\":9007199254741000,\"input_tokens_details\":{\"cached_tokens\":3}},\"completed_at\":null,\"previous_response_id\":null,\"output\":[" + reasoningDone + "," + messageDone + "]}}\n\n"
	var usage *llm.Usage
	final, err := parseStream(context.Background(), strings.NewReader(stream), func(event llm.RoundEvent) {
		if event.Kind == "usage" {
			usage = event.Usage
		}
	}, testIdentity(), "sol", "", 0)
	if err != nil {
		t.Fatalf("nulls and unknown fields rejected: %v", err)
	}
	if final.Finish != "stop" || final.Content != "hi" {
		t.Fatalf("round lost: %+v", final)
	}
	if usage == nil || usage.PromptTokens != 9007199254740993 || usage.CompletionTokens != 7 || usage.TotalTokens != 9007199254741000 {
		t.Fatalf("exact token counts lost: %+v", usage)
	}
	if final.Continuation == nil || len(final.Continuation.Items) != 2 {
		t.Fatalf("continuation lost: %+v", final.Continuation)
	}
	if string(final.Continuation.Items[0]) != reasoningDone {
		t.Fatalf("reasoning item bytes not verbatim:\n got %s\nwant %s", final.Continuation.Items[0], reasoningDone)
	}
	if string(final.Continuation.Items[1]) != messageDone {
		t.Fatalf("message item bytes not verbatim:\n got %s\nwant %s", final.Continuation.Items[1], messageDone)
	}
}

func TestWireMalformedTypedFieldsRejected(t *testing.T) {
	cases := []struct {
		name   string
		stream string
	}{
		{"string output index", "data: {\"type\":\"response.output_item.added\",\"output_index\":\"3\",\"item\":{\"type\":\"message\"}}\n\n"},
		{"numeric status", "data: {\"type\":\"response.completed\",\"response\":{\"status\":42}}\n\n"},
		{"array response", "data: {\"type\":\"response.completed\",\"response\":[{\"status\":\"completed\"}]}\n\n"},
		{"numeric usage field", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":\"many\"},\"output\":[]}}\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseStream(context.Background(), strings.NewReader(tc.stream), func(llm.RoundEvent) {}, testIdentity(), "sol", "", 0)
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
				t.Fatalf("got %v, want protocol error", err)
			}
		})
	}
}

func TestTerminalBackfillNumericConflict(t *testing.T) {
	streamFor := func(backfillValue string) string {
		return "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\"}}\n\n" +
			"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m1\",\"status\":\"completed\",\"role\":\"assistant\",\"k\":9007199254740993,\"content\":[{\"type\":\"output_text\",\"text\":\"x\"}]}}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"m1\",\"status\":\"completed\",\"role\":\"assistant\",\"k\":" + backfillValue + ",\"content\":[{\"type\":\"output_text\",\"text\":\"x\"}]}]}}\n\n"
	}
	// adjacent integers beyond float64's exact range must not compare
	// equal: a changed finalized field is surfaced, not concealed.
	_, err := parseStream(context.Background(), strings.NewReader(streamFor("9007199254740992")), func(llm.RoundEvent) {}, testIdentity(), "sol", "", 0)
	var roundErr *llm.RoundError
	if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
		t.Fatalf("adjacent large integers in backfill concealed: got %v", err)
	}
	// identical backfill completes and returns the finalized bytes verbatim.
	final, err := parseStream(context.Background(), strings.NewReader(streamFor("9007199254740993")), func(llm.RoundEvent) {}, testIdentity(), "sol", "", 0)
	if err != nil {
		t.Fatalf("identical backfill rejected: %v", err)
	}
	want := `{"type":"message","id":"m1","status":"completed","role":"assistant","k":9007199254740993,"content":[{"type":"output_text","text":"x"}]}`
	if len(final.Continuation.Items) != 1 || string(final.Continuation.Items[0]) != want {
		t.Fatalf("backfill re-encoded finalized bytes:\n got %s\nwant %s", final.Continuation.Items[0], want)
	}
}

func TestNumericToolSchemaSurvivesRequestEncoding(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	a, err := New(context.Background(), "sol", "gpt-5.6-sol", "", creds, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := baseRequest()
	request.Tools = []llm.Tool{{Type: "function", Function: &llm.FunctionDef{Name: "big", Description: "big numbers", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"minimum":-9007199254740993,"maximum":9007199254740993,"multipleOf":0.1}}}`)}}}
	body, err := a.buildRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{"-9007199254740993", "9007199254740993", "0.1"} {
		if !strings.Contains(string(body), literal) {
			t.Fatalf("numeric literal %s lost in request body: %s", literal, body)
		}
	}
	tree, err := dd.DecodeStrictJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	tools := tree["tools"].([]any)
	schema := tools[0].(map[string]any)["parameters"].(map[string]any)["properties"].(map[string]any)["a"].(map[string]any)
	if schema["minimum"] != json.Number("-9007199254740993") || schema["maximum"] != json.Number("9007199254740993") || schema["multipleOf"] != json.Number("0.1") {
		t.Fatalf("numeric schema constraints altered: %#v", schema)
	}
}

func TestRequestFieldPresence(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	plain, err := New(context.Background(), "sol", "gpt-5.6-sol", "", creds, nil)
	if err != nil {
		t.Fatal(err)
	}
	minimal := baseRequest()
	minimal.Tools = nil
	body, err := plain.buildRequest(minimal)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := dd.DecodeStrictJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if tree["store"] != false || tree["stream"] != true || tree["instructions"] != "" {
		t.Fatalf("required request fields missing or wrong: %#v", tree)
	}
	if include := tree["include"].([]any); len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("encrypted content inclusion changed: %#v", tree["include"])
	}
	for _, absent := range []string{"tools", "tool_choice", "parallel_tool_calls", "reasoning"} {
		if _, ok := tree[absent]; ok {
			t.Fatalf("field %s present without a setting: %s", absent, body)
		}
	}
	withEffort, err := New(context.Background(), "sol", "gpt-5.6-sol", "low", creds, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err = withEffort.buildRequest(baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	tree, err = dd.DecodeStrictJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	reasoning := tree["reasoning"].(map[string]any)
	if reasoning["effort"] != "low" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning object wrong: %#v", reasoning)
	}
	if tree["tool_choice"] != "auto" || tree["parallel_tool_calls"] != true {
		t.Fatalf("tool controls wrong: %#v", tree)
	}
	tools := tree["tools"].([]any)
	def := tools[0].(map[string]any)
	if def["type"] != "function" || def["name"] != "add" || def["strict"] != false {
		t.Fatalf("tool definition wrong: %#v", def)
	}
}

func TestCaptureContinuationOutgoingRequestRoundTrip(t *testing.T) {
	creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
	reasoningSpan := `{"type":"reasoning","id":"rs_1","status":"completed","encrypted_content":"ENC\u00e9\u001fA","summary":[],"seq":9007199254740993}`
	callSpan := `{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"add","arguments":"{\"a\":1}"}`
	messageSpan := `{"type":"message","id":"msg_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done","annotations":[]}]}`
	roundOne := "data: {\"type\":\"response.created\",\"response\":{\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":" + reasoningSpan + "}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + reasoningSpan + "}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":" + callSpan + "}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":" + callSpan + "}\n\n" +
		"data: {\"type\":\"response.output_item.added\",\"output_index\":2,\"item\":" + messageSpan + "}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":2,\"item\":" + messageSpan + "}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[" + reasoningSpan + "," + callSpan + "," + messageSpan + "]}}\n\n"
	var bodies [][]byte
	transport := transportFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, body)
		if len(bodies) == 1 {
			return sseResponse(roundOne), nil
		}
		return sseResponse(responseText("4")), nil
	})
	a := newAdapter(t, creds, transport)
	final, err := a.Round(context.Background(), baseRequest(), func(llm.RoundEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if final.Finish != "tool_calls" || final.Content != "done" || len(final.Calls) != 1 {
		t.Fatalf("round one lost: %+v", final)
	}
	if final.Continuation == nil || len(final.Continuation.Items) != 3 {
		t.Fatalf("continuation lost: %+v", final.Continuation)
	}
	// saved continuation keeps the provider's exact bytes: escape
	// spellings, key order, and unknown fields are not re-serialized.
	if string(final.Continuation.Items[0]) != reasoningSpan || string(final.Continuation.Items[1]) != callSpan || string(final.Continuation.Items[2]) != messageSpan {
		t.Fatalf("continuation bytes not verbatim:\n%q\n%q\n%q", final.Continuation.Items[0], final.Continuation.Items[1], final.Continuation.Items[2])
	}
	assistant := llm.Message{
		Role:    "assistant",
		Content: llm.StringContent("done"),
		ToolCalls: []llm.ToolCall{{
			ID: final.Calls[0].ID, Type: "function",
			Function: llm.ToolCallFunction{Name: final.Calls[0].Name, Arguments: final.Calls[0].Arguments},
		}},
		Origin: final.Origin, Continuation: final.Continuation,
	}
	request := baseRequest()
	request.Messages = append(request.Messages, assistant, llm.Message{Role: "tool", ToolCallID: final.Calls[0].ID, Content: llm.StringContent("4")})
	if _, err := a.Round(context.Background(), request, func(llm.RoundEvent) {}); err != nil {
		t.Fatal(err)
	}
	tree, err := dd.DecodeStrictJSON(bodies[1])
	if err != nil {
		t.Fatal(err)
	}
	input := tree["input"].([]any)
	if len(input) != 5 {
		t.Fatalf("outgoing input shape wrong: %#v", input)
	}
	reasoningItem := input[1].(map[string]any)
	if reasoningItem["encrypted_content"] != "ENC\xc3\xa9\x1fA" {
		t.Fatalf("encrypted string value changed in outgoing request: %#v", reasoningItem["encrypted_content"])
	}
	if _, ok := reasoningItem["summary"]; !ok {
		t.Fatalf("unknown provider field dropped from outgoing item: %#v", reasoningItem)
	}
	if reasoningItem["seq"] != json.Number("9007199254740993") {
		t.Fatalf("exact number changed in outgoing item: %#v", reasoningItem["seq"])
	}
	callItem := input[2].(map[string]any)
	if callItem["id"] != "fc_1" || callItem["call_id"] != "call_1" || callItem["name"] != "add" {
		t.Fatalf("function call item changed in outgoing request: %#v", callItem)
	}
	messageItem := input[3].(map[string]any)
	if messageItem["id"] != "msg_1" {
		t.Fatalf("message item changed in outgoing request: %#v", messageItem)
	}
	outputItem := input[4].(map[string]any)
	if outputItem["type"] != "function_call_output" || outputItem["call_id"] != "call_1" || outputItem["output"] != "4" {
		t.Fatalf("tool output association changed: %#v", outputItem)
	}
}

// malformedOutputStream is one completed streamed function call followed by a
// terminal event whose output member has the given spelling.
func malformedOutputStream(output string) string {
	item := `{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"add","arguments":"{}"}`
	return "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":" + item + "}\n\n" +
		"data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":" + item + "}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":" + output + "}}\n\n"
}

func TestTerminalOutputMalformedShapesRejected(t *testing.T) {
	callSpan := `{"type":"function_call","id":"fc_1","status":"completed","call_id":"call_1","name":"add","arguments":"{}"}`
	cases := []struct {
		name   string
		output string
	}{
		{"object output", `{}`},
		{"string output", `"output"`},
		{"number output", `5`},
		{"null element", `[null]`},
		{"string element", `["a call"]`},
		{"number element", `[1]`},
		{"nested array element", `[[1]]`},
		{"mixed valid null", `[` + callSpan + `,null]`},
		{"mixed null valid", `[null,` + callSpan + `]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			final, err := parseStream(context.Background(), strings.NewReader(malformedOutputStream(tc.output)), func(llm.RoundEvent) {}, testIdentity(), "sol", "", 0)
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
				t.Fatalf("got %v, want protocol error (finish=%s calls=%d)", err, final.Finish, len(final.Calls))
			}
		})
	}
}

func TestTerminalEmptyOutputPreservesStreamedCalls(t *testing.T) {
	for _, output := range []string{`[]`, `null`} {
		t.Run(output, func(t *testing.T) {
			final, err := parseStream(context.Background(), strings.NewReader(malformedOutputStream(output)), func(llm.RoundEvent) {}, testIdentity(), "sol", "", 0)
			if err != nil {
				t.Fatalf("valid omitted/empty output rejected: %v", err)
			}
			if final.Finish != "tool_calls" || len(final.Calls) != 1 || final.Calls[0].Name != "add" || final.Continuation == nil || len(final.Continuation.Items) != 1 {
				t.Fatalf("streamed completed call lost: %+v", final)
			}
		})
	}
}

func TestMalformedTerminalOutputExecutesNothing(t *testing.T) {
	for _, output := range []string{`{}`, `[null]`} {
		t.Run(output, func(t *testing.T) {
			creds := &fakeCredentials{account: "acct-pane-test-a", token: "A"}
			stream := malformedOutputStream(output)
			a := newAdapter(t, creds, transportFunc(func(*http.Request) (*http.Response, error) {
				return sseResponse(stream), nil
			}))
			executor := &fakeExecutor{}
			request := baseRequest()
			err := llm.RunToolLoop(context.Background(), a, llm.Turn{}, request.Messages, request.Model, 0, request.Tools, executor, loopSink(func(llm.LoopEvent) error { return nil }), nil)
			var roundErr *llm.RoundError
			if !errors.As(err, &roundErr) || roundErr.Kind != "protocol" {
				t.Fatalf("got %v, want protocol error", err)
			}
			if executor.calls != 0 {
				t.Fatalf("malformed terminal round executed %d tools", executor.calls)
			}
		})
	}
}
