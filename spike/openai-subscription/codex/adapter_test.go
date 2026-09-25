package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

const testToken = "TEST-ACCESS-TOKEN-must-not-leak"

type fakeCreds struct {
	account string
	fail    bool
}

func (f *fakeCreds) Access(context.Context) (string, string, error) {
	if f.fail {
		return "", "", errors.New("login required")
	}
	return testToken, f.account, nil
}

func (f *fakeCreds) AccountScope(context.Context) (string, error) {
	if f.fail {
		return "", errors.New("login required")
	}
	return "chatgpt:scope-" + f.account, nil
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "codex", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// sseServer replays scripted responses in order and captures request bodies.
type sseServer struct {
	*httptest.Server
	bodies  [][]byte
	headers []http.Header
	hits    atomic.Int32
}

func newSSEServer(t *testing.T, responses ...func(w http.ResponseWriter)) *sseServer {
	s := &sseServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(s.hits.Add(1)) - 1
		b, _ := io.ReadAll(r.Body)
		s.bodies = append(s.bodies, b)
		s.headers = append(s.headers, r.Header.Clone())
		if n >= len(responses) {
			http.Error(w, "unexpected request", http.StatusTeapot)
			return
		}
		responses[n](w)
	}))
	t.Cleanup(s.Close)
	return s
}

func sse(body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}
}

func status(code int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}
}

func testAdapter(t *testing.T, srv *sseServer, model, account string) *Adapter {
	t.Helper()
	a, err := newAdapter(Config{Alias: model, UpstreamModel: model, Effort: "low"}, &fakeCreds{account: account}, nil, srv.URL+"/backend-api/codex/responses")
	if err != nil {
		t.Fatal(err)
	}
	seq := 0
	a.newCallID = func() string { seq++; return fmt.Sprintf("pane_call_%d", seq) }
	return a
}

func userHistory(text string) []round.Message {
	return []round.Message{
		{Role: "system", Content: round.Str("you are terse.")},
		{Role: "user", Content: round.Str(text)},
	}
}

var addTool = []round.Tool{{Name: "add", Description: "add two integers", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}`)}}

func decodeInput(t *testing.T, body []byte) (map[string]any, []map[string]any) {
	t.Helper()
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	for _, it := range req["input"].([]any) {
		items = append(items, it.(map[string]any))
	}
	return req, items
}

func TestStreamNormalizationAndFinal(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "answer.sse")))
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	var events []round.Event
	final, err := a.Round(context.Background(), round.Request{History: userHistory("2+2?")}, func(e round.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if final.Finish != round.FinishStop || final.RawFinish != "completed" {
		t.Fatalf("finish %q raw %q", final.Finish, final.RawFinish)
	}
	if *final.Assistant.Content != "four." {
		t.Fatalf("content %q", *final.Assistant.Content)
	}
	u := final.Usage
	if u == nil || u.InputTokens != 120 || u.CachedTokens != 64 || u.OutputTokens != 30 || u.ReasoningTokens != 22 || u.TotalTokens != 150 {
		t.Fatalf("usage %+v", u)
	}
	kinds := []round.EventKind{}
	for _, e := range events {
		kinds = append(kinds, e.Kind)
	}
	want := []round.EventKind{round.EventThinking, round.EventThinking, round.EventText, round.EventText}
	if fmt.Sprint(kinds) != fmt.Sprint(want) {
		t.Fatalf("events %v", kinds)
	}
	env := final.Assistant.Continuation
	if env == nil || len(env.Items) != 2 || env.Format != EnvelopeFormat || env.Version != 1 {
		t.Fatalf("envelope %+v", env)
	}
	if !strings.Contains(string(env.Items[0]), "ENC_SYNTH_1") {
		t.Fatal("encrypted reasoning not preserved")
	}
	if final.Assistant.Origin.Identity.AccountScope != "chatgpt:scope-A" {
		t.Fatal("account scope missing from identity")
	}
	// request shape: store=false, stream, include encrypted reasoning, effort
	req, items := decodeInput(t, srv.bodies[0])
	if req["store"] != false || req["stream"] != true || req["instructions"] != "you are terse." {
		t.Fatalf("request %v", req)
	}
	if fmt.Sprint(req["include"]) != "[reasoning.encrypted_content]" {
		t.Fatal("include missing")
	}
	if req["reasoning"].(map[string]any)["effort"] != "low" {
		t.Fatal("effort not sent")
	}
	if _, ok := req["max_output_tokens"]; ok {
		t.Fatal("max_output_tokens sent without explicit configuration")
	}
	if len(items) != 1 || items[0]["role"] != "user" {
		t.Fatalf("input %v", items)
	}
	h := srv.headers[0]
	if h.Get("Authorization") != "Bearer "+testToken || h.Get("chatgpt-account-id") != "A" || h.Get("originator") != "pane" {
		t.Fatal("headers not set by locked transport")
	}
}

func TestToolCallRoundAndEnvelopeReplay(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "toolcall.sse")), sse(fixture(t, "answer.sse")))
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	history := userHistory("add 2 and 2")
	final, err := a.Round(context.Background(), round.Request{History: history, Tools: addTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Executable() || len(final.Assistant.ToolCalls) != 1 {
		t.Fatalf("expected executable tool round, got %+v", final)
	}
	tc := final.Assistant.ToolCalls[0]
	if tc.ID != "pane_call_1" || tc.Name != "add" || tc.Arguments != `{"a": 2, "b": 2}` {
		t.Fatalf("tool call %+v", tc)
	}
	b := final.Assistant.Continuation.Bindings[0]
	if b.CallID != "pane_call_1" || b.ProviderCallID != "call_SYNTH_2" || b.ProviderItemID != "fc_SYNTH_2" {
		t.Fatalf("binding %+v", b)
	}

	history = append(history, final.Assistant, round.Message{Role: "tool", ToolCallID: tc.ID, Content: round.Str("4")})
	if _, err := a.Round(context.Background(), round.Request{History: history, Tools: addTool}, nil); err != nil {
		t.Fatal(err)
	}
	_, items := decodeInput(t, srv.bodies[1])
	types := []string{}
	for _, it := range items {
		types = append(types, fmt.Sprint(it["type"], "/", it["role"]))
	}
	if fmt.Sprint(types) != "[<nil>/user reasoning/<nil> function_call/<nil> function_call_output/<nil>]" {
		t.Fatalf("items %v", types)
	}
	if items[1]["encrypted_content"] != "ENC_SYNTH_2" || items[2]["id"] != "fc_SYNTH_2" || items[2]["call_id"] != "call_SYNTH_2" {
		t.Fatalf("replayed items %v %v", items[1], items[2])
	}
	if items[3]["call_id"] != "call_SYNTH_2" || items[3]["output"] != "4" {
		t.Fatalf("output pairing %v", items[3])
	}
}

func TestSerializeReloadProducesIdenticalRequest(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "toolcall.sse")))
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	history := userHistory("add 2 and 2")
	final, err := a.Round(context.Background(), round.Request{History: history, Tools: addTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	history = append(history, final.Assistant, round.Message{Role: "tool", ToolCallID: final.Assistant.ToolCalls[0].ID, Content: round.Str("4")})
	before, _, err := a.BuildBody(round.Request{History: history, Tools: addTool}, a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := json.Marshal(history)
	var reloaded []round.Message
	if err := json.Unmarshal(stored, &reloaded); err != nil {
		t.Fatal(err)
	}
	fresh := testAdapter(t, srv, "gpt-5.6-sol", "A")
	after, decisions, err := fresh.BuildBody(round.Request{History: reloaded, Tools: addTool}, fresh.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("reloaded request differs:\n%s\n%s", before, after)
	}
	if len(decisions) != 1 || !decisions[0].Replayed {
		t.Fatalf("decisions %+v", decisions)
	}
}

func TestEnvelopeFilteredOnIdentityChange(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "toolcall.sse")))
	sol := testAdapter(t, srv, "gpt-5.6-sol", "A")
	final, err := sol.Round(context.Background(), round.Request{History: userHistory("x"), Tools: addTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	history := append(userHistory("x"), final.Assistant, round.Message{Role: "tool", ToolCallID: final.Assistant.ToolCalls[0].ID, Content: round.Str("4")})

	cases := map[string]*Adapter{
		"different model":   testAdapter(t, srv, "gpt-6-astra", "A"),
		"different account": testAdapter(t, srv, "gpt-5.6-sol", "B"),
	}
	for name, other := range cases {
		body, decisions, err := other.BuildBody(round.Request{History: history, Tools: addTool}, other.Identity())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if decisions[0].Replayed || decisions[0].Reason != "identity mismatch" {
			t.Fatalf("%s: decision %+v", name, decisions[0])
		}
		s := string(body)
		if strings.Contains(s, "ENC_SYNTH") || strings.Contains(s, "fc_SYNTH") || strings.Contains(s, "reasoning\"") && strings.Contains(s, "\"type\":\"reasoning\"") {
			t.Fatalf("%s: opaque data leaked: %s", name, s)
		}
		_, items := decodeInput(t, body)
		if items[1]["type"] != "function_call" || items[1]["call_id"] != "pane_call_1" || items[2]["call_id"] != "pane_call_1" {
			t.Fatalf("%s: portable pairing broken: %v", name, items)
		}
	}
	// the stored message is untouched by filtering.
	if history[2].Continuation == nil || len(history[2].Continuation.Items) != 2 {
		t.Fatal("stored continuation was mutated")
	}
}

func TestEnvelopeValidationFailsClosed(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "toolcall.sse")))
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	final, err := a.Round(context.Background(), round.Request{History: userHistory("x"), Tools: addTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := final.Assistant
	clone := func() round.Message {
		b, _ := json.Marshal(base)
		var m round.Message
		_ = json.Unmarshal(b, &m)
		return m
	}
	mutations := map[string]func(m *round.Message){
		"version":          func(m *round.Message) { m.Continuation.Version = 2 },
		"format":           func(m *round.Message) { m.Continuation.Format = "other" },
		"args edited":      func(m *round.Message) { m.ToolCalls[0].Arguments = `{"a":3,"b":2}` },
		"binding swapped":  func(m *round.Message) { m.Continuation.Bindings[0].ProviderCallID = "call_other" },
		"reasoning last":   func(m *round.Message) { m.Continuation.Items = m.Continuation.Items[:1] },
		"unknown item":     func(m *round.Message) { m.Continuation.Items[0] = json.RawMessage(`{"type":"web_search_call"}`) },
		"malformed item":   func(m *round.Message) { m.Continuation.Items[0] = json.RawMessage(`{`) },
		"text mismatch":    func(m *round.Message) { m.Content = round.Str("injected") },
		"origin missing":   func(m *round.Message) { m.Origin = nil },
		"origin disagrees": func(m *round.Message) { m.Origin.Identity.UpstreamModel = "gpt-6-astra" },
	}
	for name, mutate := range mutations {
		m := clone()
		mutate(&m)
		history := append(userHistory("x"), m, round.Message{Role: "tool", ToolCallID: m.ToolCalls[0].ID, Content: round.Str("4")})
		body, decisions, err := a.BuildBody(round.Request{History: history, Tools: addTool}, a.Identity())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if decisions[0].Replayed {
			t.Fatalf("%s: tampered envelope was replayed", name)
		}
		if strings.Contains(string(body), "ENC_SYNTH_2") {
			t.Fatalf("%s: opaque data sent", name)
		}
	}
}

func TestLegacyHistoryAndForeignReasoningDropped(t *testing.T) {
	srv := newSSEServer(t)
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	history := []round.Message{
		{Role: "system", Content: round.Str("sys")},
		{Role: "user", Content: round.Str("hi")},
		// legacy: no origin at all
		{Role: "assistant", Content: round.Str("hello"), ToolCalls: []round.ToolCall{{ID: "pane_call_7_0_0", Name: "add", Arguments: `{"a":1,"b":1}`}}},
		{Role: "tool", ToolCallID: "pane_call_7_0_0", Content: round.Str("2")},
		{Role: "user", Content: round.Str("thanks")},
	}
	body, decisions, err := a.BuildBody(round.Request{History: history, Tools: addTool}, a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if decisions[0].Reason != "no provenance (legacy history)" {
		t.Fatalf("decision %+v", decisions[0])
	}
	_, items := decodeInput(t, body)
	if items[1]["type"] != "message" || items[1]["id"] != "msg_pane_2" || items[2]["call_id"] != "pane_call_7_0_0" || items[3]["call_id"] != "pane_call_7_0_0" {
		t.Fatalf("items %v", items)
	}
	if _, has := items[2]["id"]; has {
		t.Fatal("portable function_call must omit provider item id")
	}
}

func TestForcedFinalAndOrphans(t *testing.T) {
	srv := newSSEServer(t)
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	history := []round.Message{
		{Role: "system", Content: round.Str("sys")},
		{Role: "user", Content: round.Str("hi")},
		{Role: "assistant", ToolCalls: []round.ToolCall{{ID: "c1", Name: "add", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "c1", Content: round.Str("error: x")},
	}
	body, _, err := a.BuildBody(round.Request{History: history, Tools: addTool, Intent: round.IntentForcedFinal}, a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	req, _ := decodeInput(t, body)
	if _, ok := req["tools"]; ok {
		t.Fatal("tools sent on forced-final round")
	}
	if !strings.HasSuffix(req["instructions"].(string), round.ForcedFinalInstruction) || !strings.HasPrefix(req["instructions"].(string), "sys") {
		t.Fatalf("instructions %q", req["instructions"])
	}

	orphan := history[:3]
	if _, _, err := a.BuildBody(round.Request{History: orphan}, a.Identity()); round.KindOf(err) != round.ErrInvalidRequest {
		t.Fatalf("orphaned call not rejected locally: %v", err)
	}
	if srv.hits.Load() != 0 {
		t.Fatal("request sent")
	}
}

func TestFaultsNeverYieldExecutableRounds(t *testing.T) {
	cases := []struct {
		name  string
		resp  func(http.ResponseWriter)
		kind  round.ErrorKind
		calls int
	}{
		{"truncated mid tool call", sse(fixture(t, "truncated.sse")), round.ErrTruncated, 1},
		{"malformed arguments", sse(fixture(t, "malformed-args.sse")), round.ErrIncomplete, 1},
		{"incomplete max_output_tokens", sse(fixture(t, "incomplete.sse")), round.ErrIncomplete, 0},
		{"failed allowance", sse(fixture(t, "failed-allowance.sse")), round.ErrAllowance, 0},
		{"invalid json", sse("data: {nope\n\n"), round.ErrProtocol, 0},
		{"http 429 usage limit", status(429, `{"error":{"code":"usage_limit_reached","message":"synthetic","plan_type":"plus","resets_at":1}}`), round.ErrAllowance, 0},
		{"http 429 rate limit", status(429, `{"error":{"code":"rate_limit_exceeded","message":"slow down"}}`), round.ErrRateLimited, 0},
		{"http 401", status(401, `{"error":{"message":"token revoked"}}`), round.ErrAuth, 0},
		{"http 400", status(400, `{"error":{"type":"invalid_request_error","message":"bad"}}`), round.ErrInvalidRequest, 0},
		{"http 403 model", status(403, `{"detail":"model not available on plan"}`), round.ErrModelAccess, 0},
		{"http 502", status(502, `bad gateway`), round.ErrUpstream, 0},
	}
	for _, c := range cases {
		srv := newSSEServer(t, c.resp)
		a := testAdapter(t, srv, "gpt-5.6-sol", "A")
		final, err := a.Round(context.Background(), round.Request{History: userHistory("x"), Tools: addTool}, nil)
		if final != nil {
			t.Fatalf("%s: got a final round", c.name)
		}
		var re *round.Error
		if !errors.As(err, &re) || re.Kind != c.kind {
			t.Fatalf("%s: got %v, want kind %s", c.name, err, c.kind)
		}
		if len(re.Partial.ToolCalls) != c.calls {
			t.Fatalf("%s: partial calls %d", c.name, len(re.Partial.ToolCalls))
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("%s: token leaked into error", c.name)
		}
		if srv.hits.Load() != 1 {
			t.Fatalf("%s: %d requests; the adapter must not retry", c.name, srv.hits.Load())
		}
	}
}

func TestReasoningBackfill(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "backfill.sse")))
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	final, err := a.Round(context.Background(), round.Request{History: userHistory("x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(final.Assistant.Continuation.Items[0]), "ENC_SYNTH_5") {
		t.Fatal("encrypted content not backfilled from terminal response")
	}
}

func TestCancellationMidStream(t *testing.T) {
	release := make(chan struct{})
	srv := newSSEServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join(strings.SplitAfter(fixture(t, "toolcall.sse"), "\n\n")[:5], ""))
		w.(http.Flusher).Flush()
		<-release
	})
	defer close(release)
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{}, 1)
	go func() { <-started; cancel() }()
	_, err := a.Round(ctx, round.Request{History: userHistory("x"), Tools: addTool}, func(e round.Event) {
		if e.Kind == round.EventToolCallArgs {
			select {
			case started <- struct{}{}:
			default:
			}
		}
	})
	if round.KindOf(err) != round.ErrCancelled {
		t.Fatalf("got %v", err)
	}
}

func TestDestinationIsolation(t *testing.T) {
	var otherHits atomic.Int32
	var sawAuth atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHits.Add(1)
		if r.Header.Get("Authorization") != "" {
			sawAuth.Store(true)
		}
	}))
	defer other.Close()
	redirector := newSSEServer(t, func(w http.ResponseWriter) {
		w.Header().Set("Location", other.URL+"/steal")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	allowed := redirector.URL + "/backend-api/codex/responses"
	client := newLockedClient(nil, &fakeCreds{account: "A"}, allowed)

	for _, target := range []string{other.URL + "/backend-api/codex/responses", allowed + "?x=1", strings.Replace(allowed, "/responses", "/responses2", 1)} {
		req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader("{}"))
		if _, err := client.Do(req); !errors.Is(err, ErrDestination) {
			t.Fatalf("%s: expected destination refusal, got %v", target, err)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, allowed, strings.NewReader("{}"))
	if _, err := client.Do(req); !errors.Is(err, ErrDestination) {
		t.Fatalf("redirect not refused: %v", err)
	}
	if otherHits.Load() != 0 || sawAuth.Load() {
		t.Fatal("credentials or requests reached a foreign destination")
	}

	// the production constructor is pinned to the fixed destination.
	a, err := New(Config{Alias: "s", UpstreamModel: "gpt-5.6-sol"}, &fakeCreds{account: "A"}, nil)
	if err != nil || a.url != ResponsesURL {
		t.Fatalf("production adapter destination %q", a.url)
	}
}

func TestEffortValidation(t *testing.T) {
	ok := [][2]string{{"gpt-5.6-sol", ""}, {"gpt-5.6-sol", "none"}, {"gpt-5.6-sol", "max"}, {"gpt-6-astra", "xhigh"}}
	bad := [][2]string{{"gpt-6-astra", "none"}, {"gpt-5.6-sol", "minimal"}, {"gpt-5.6-sol", "off"}, {"gpt-9", "low"}}
	for _, c := range ok {
		if err := ValidateEffort(c[0], c[1]); err != nil {
			t.Fatalf("%v: %v", c, err)
		}
	}
	for _, c := range bad {
		if err := ValidateEffort(c[0], c[1]); err == nil {
			t.Fatalf("%v accepted", c)
		}
	}
}

func TestSignedOutFailsBeforeSending(t *testing.T) {
	srv := newSSEServer(t)
	a, _ := newAdapter(Config{Alias: "s", UpstreamModel: "gpt-5.6-sol"}, &fakeCreds{fail: true}, nil, srv.URL+"/r")
	if _, err := a.Round(context.Background(), round.Request{History: userHistory("x")}, nil); round.KindOf(err) != round.ErrAuth {
		t.Fatalf("got %v", err)
	}
	if srv.hits.Load() != 0 {
		t.Fatal("request sent while signed out")
	}
}

// TestLiveShapeToolRound parses a sanitized capture of a real sol tool round
// (2026-09-23): ids renamed, identifiers redacted, attribution removed.
func TestLiveShapeToolRound(t *testing.T) {
	srv := newSSEServer(t, sse(fixture(t, "live-shape-toolcall.sse")))
	a := testAdapter(t, srv, "gpt-5.6-sol", "A")
	final, err := a.Round(context.Background(), round.Request{History: userHistory("x"), Tools: addTool}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !final.Executable() || final.Assistant.ToolCalls[0].Name != "add" || final.Assistant.ToolCalls[0].Arguments != `{"a":17,"b":25}` {
		t.Fatalf("final %+v", final.Assistant.ToolCalls)
	}
	if final.Usage == nil || final.Usage.InputTokens != 83 || final.Usage.OutputTokens != 21 {
		t.Fatalf("usage %+v", final.Usage)
	}
	if b := final.Assistant.Continuation.Bindings[0]; b.ProviderCallID != "call_SANITIZED_3" || b.ProviderItemID == "" {
		t.Fatalf("binding %+v", b)
	}
}
