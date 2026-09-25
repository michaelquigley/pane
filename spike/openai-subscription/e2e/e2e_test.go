// Package e2e drives the real adapters through the destination-locked
// transport against local mock upstreams, the real mcp fixture, and a fresh
// OS process for reload. mock success is evidence of local handling only.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/michaelquigley/pane/spike/openai-subscription/codex"
	"github.com/michaelquigley/pane/spike/openai-subscription/convo"
	"github.com/michaelquigley/pane/spike/openai-subscription/loop"
	"github.com/michaelquigley/pane/spike/openai-subscription/mcpfix"
	"github.com/michaelquigley/pane/spike/openai-subscription/qwen"
	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

var fixtureBin string

func TestMain(m *testing.M) {
	if os.Getenv("PANE_SPIKE_E2E_CHILD") == "1" {
		os.Exit(childContinue())
	}
	dir, err := os.MkdirTemp("", "pane-spike-e2e-")
	if err != nil {
		panic(err)
	}
	fixtureBin = filepath.Join(dir, "mcpadd")
	if out, err := exec.Command("go", "build", "-o", fixtureBin, "../mcpfix/mcpadd").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type creds struct{ account string }

func (c creds) Access(context.Context) (string, string, error) { return "E2E-TOKEN", c.account, nil }
func (c creds) AccountScope(context.Context) (string, error)   { return "chatgpt:e2e-" + c.account, nil }

// upstream is a scripted mock: each request pops the next canned body.
type upstream struct {
	mu      sync.Mutex
	srv     *httptest.Server
	scripts []string
	bodies  []map[string]any
}

func newUpstream(t *testing.T, scripts ...string) *upstream {
	u := &upstream{scripts: scripts}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		u.bodies = append(u.bodies, b)
		if len(u.scripts) == 0 {
			http.Error(w, "no script", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, u.scripts[0])
		u.scripts = u.scripts[1:]
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// redirectTo rewrites the already-authorized request to the mock. the
// locked transport has checked the real destination before this runs.
func redirectTo(target string) http.RoundTripper {
	tu, _ := url.Parse(target)
	return rtFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != codex.ResponsesURL {
			return nil, fmt.Errorf("unexpected destination %s", r.URL)
		}
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host = tu.Scheme, tu.Host
		r2.Host = tu.Host
		return http.DefaultTransport.RoundTrip(r2)
	})
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fixtureSSE(name string) string {
	b, err := os.ReadFile(filepath.Join("..", "testdata", "codex", name))
	if err != nil {
		panic(err)
	}
	return string(b)
}

func codexAdapter(t testing.TB, model, account string, up *upstream) *codex.Adapter {
	a, err := codex.New(codex.Config{Alias: model, UpstreamModel: model, Effort: "low"}, creds{account}, redirectTo(up.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func itemTypes(body map[string]any) []string {
	var out []string
	for _, it := range body["input"].([]any) {
		m := it.(map[string]any)
		if t, ok := m["type"]; ok {
			out = append(out, t.(string))
		} else {
			out = append(out, "role:"+m["role"].(string))
		}
	}
	return out
}

// TestDurableContinuationAcrossProcesses: tool round with the real fixture,
// save, then a fresh OS process reloads and continues from full history.
func TestDurableContinuationAcrossProcesses(t *testing.T) {
	log := filepath.Join(t.TempDir(), "exec.log")
	fx, err := mcpfix.Start(context.Background(), fixtureBin, []string{"PANE_SPIKE_EXEC_LOG=" + log})
	if err != nil {
		t.Fatal(err)
	}
	defer fx.Close()

	up := newUpstream(t, fixtureSSE("toolcall.sse"), fixtureSSE("answer.sse"))
	a := codexAdapter(t, "gpt-5.6-sol", "A", up)
	doc := &convo.Document{Title: "e2e", Messages: []round.Message{{Role: "system", Content: round.Str("sys")}, {Role: "user", Content: round.Str("add 2 and 2")}}}
	path := filepath.Join(t.TempDir(), "conversation.json")
	res, err := loop.RunTurn(context.Background(), a, doc.Messages, fx.Tools(), fx, loop.ApproveAll{}, convo.NewFileRecorder(path, doc), loop.Options{})
	if err != nil {
		t.Fatal(err)
	}
	doc.Messages = append(doc.Messages, res.Messages...)
	if err := convo.Save(path, doc); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(log); strings.Count(string(b), "\n") != 1 {
		t.Fatal("fixture did not execute exactly once")
	}
	if got := itemTypes(up.bodies[1]); fmt.Sprint(got) != "[role:user reasoning function_call function_call_output]" {
		t.Fatalf("in-turn replay %v", got)
	}

	// fresh process: new adapter, no shared memory, only the file.
	child := newUpstream(t, fixtureSSE("answer.sse"))
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "PANE_SPIKE_E2E_CHILD=1", "PANE_SPIKE_DOC="+path, "PANE_SPIKE_UPSTREAM="+child.srv.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	if len(child.bodies) != 1 {
		t.Fatal("child did not send exactly one request")
	}
	got := itemTypes(child.bodies[0])
	want := "[role:user reasoning function_call function_call_output reasoning message role:user]"
	if fmt.Sprint(got) != want {
		t.Fatalf("reloaded request items\n got %v\nwant %s", got, want)
	}
	raw, _ := json.Marshal(child.bodies[0])
	for _, s := range []string{"ENC_SYNTH_2", "ENC_SYNTH_1", "call_SYNTH_2"} {
		if !strings.Contains(string(raw), s) {
			t.Fatalf("continuation %s missing after reload", s)
		}
	}
	if _, has := child.bodies[0]["previous_response_id"]; has {
		t.Fatal("hidden provider session used")
	}
	final, _ := convo.Load(path)
	if len(final.Messages) != 7 || *final.Messages[6].Content != "four." {
		t.Fatalf("child did not persist its answer: %d messages", len(final.Messages))
	}
}

func childContinue() int {
	path := os.Getenv("PANE_SPIKE_DOC")
	doc, err := convo.Load(path)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	a, err := codex.New(codex.Config{Alias: "gpt-5.6-sol", UpstreamModel: "gpt-5.6-sol", Effort: "low"}, creds{"A"}, redirectTo(os.Getenv("PANE_SPIKE_UPSTREAM")))
	if err != nil {
		fmt.Println(err)
		return 1
	}
	doc.Messages = append(doc.Messages, round.Message{Role: "user", Content: round.Str("and again?")})
	res, err := loop.RunTurn(context.Background(), a, doc.Messages, nil, nil, loop.DenyAll{}, convo.NewFileRecorder(path, doc), loop.Options{})
	if err != nil {
		fmt.Println(err)
		return 1
	}
	doc.Messages = append(doc.Messages, res.Messages...)
	if err := convo.Save(path, doc); err != nil {
		fmt.Println(err)
		return 1
	}
	return 0
}

const qwenToolStream = "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"QWEN-THOUGHT\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"x\",\"function\":{\"name\":\"add\",\"arguments\":\"{\\\"a\\\":1,\\\"b\\\":2}\"}}]}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"
const qwenAnswerStream = "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"ANSWER-THOUGHT\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{\"content\":\"three\"}}]}\n\n" +
	"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"

// TestSwitchingSequence: qwen -> sol -> astra -> sol. portable answers and
// tool history carry across; opaque replay is filtered per message.
func TestSwitchingSequence(t *testing.T) {
	fx, err := mcpfix.Start(context.Background(), fixtureBin, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fx.Close()
	history := []round.Message{{Role: "system", Content: round.Str("sys")}, {Role: "user", Content: round.Str("1+2 with the tool")}}
	turn := func(a round.Adapter, user string) {
		t.Helper()
		if user != "" {
			history = append(history, round.Message{Role: "user", Content: round.Str(user)})
		}
		res, err := loop.RunTurn(context.Background(), a, history, fx.Tools(), fx, loop.ApproveAll{}, nil, loop.Options{})
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, res.Messages...)
	}

	qup := newUpstream(t, qwenToolStream, qwenAnswerStream)
	var qbodies []map[string]any
	qsrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		qbodies = append(qbodies, b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, qup.scripts[0])
		qup.scripts = qup.scripts[1:]
	}))
	defer qsrv.Close()
	q, err := qwen.New(qwen.Config{Alias: "qwen3.8-27b@fortyfive", Endpoint: qsrv.URL + "/v1", UpstreamModel: "qwen3.8-27b", Profile: "qwen3.8-llamacpp", Effort: "low", MaxTokens: 4096}, nil)
	if err != nil {
		t.Fatal(err)
	}
	turn(q, "")
	// in-turn qwen replay of active reasoning, request-local only.
	msgs := qbodies[1]["messages"].([]any)
	if msgs[2].(map[string]any)["reasoning_content"] != "QWEN-THOUGHT" {
		t.Fatalf("active qwen reasoning not replayed: %v", msgs[2])
	}
	stored, _ := json.Marshal(history)
	if strings.Contains(string(stored), "THOUGHT") {
		t.Fatal("qwen reasoning persisted")
	}

	solUp := newUpstream(t, fixtureSSE("answer.sse"), fixtureSSE("answer.sse"))
	sol := codexAdapter(t, "gpt-5.6-sol", "A", solUp)
	turn(sol, "now in words")
	if got := fmt.Sprint(itemTypes(solUp.bodies[0])); got != "[role:user function_call function_call_output message role:user]" {
		t.Fatalf("sol after qwen: %s", got)
	}

	astraUp := newUpstream(t, fixtureSSE("answer.sse"))
	astra := codexAdapter(t, "gpt-6-astra", "A", astraUp)
	turn(astra, "again")
	raw, _ := json.Marshal(astraUp.bodies[0])
	if strings.Contains(string(raw), "ENC_SYNTH") {
		t.Fatal("sol continuation sent to astra")
	}

	turn(sol, "back to sol")
	raw, _ = json.Marshal(solUp.bodies[1])
	if !strings.Contains(string(raw), "ENC_SYNTH_1") {
		t.Fatal("sol continuation not replayed after switching back")
	}
	if got := fmt.Sprint(itemTypes(solUp.bodies[1])); got != "[role:user function_call function_call_output message role:user reasoning message role:user message role:user]" {
		t.Fatalf("sol after astra: %s", got)
	}
	// every assistant message kept its origin; the stored record is intact.
	for i, m := range history {
		if m.Role == "assistant" && m.Origin == nil {
			t.Fatalf("message %d lost provenance", i)
		}
	}
}
