package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/internal/session"
)

func TestTurnLifecycleEventsAreSequencedAndBound(t *testing.T) {
	h := newTurnHarness(t)
	h.chat.script = []string{chatToolCall("add", `{"a":17,"b":25}`), chatText("the sum is 42")}
	recorder, events := h.post(t, freshTurn("qwen", "add 17 and 25"))
	if recorder.Code != http.StatusOK || h.tools.count() != 1 {
		t.Fatalf("status=%d executions=%d", recorder.Code, h.tools.count())
	}
	var critical []string
	seq := 0.0
	for _, event := range events {
		if event.Type != "done" && event.Data["turn_id"] != "t1" {
			t.Fatalf("%s is not bound to the submitted turn: %s", event.Type, event.Raw)
		}
		if s, ok := event.Data["seq"].(float64); ok {
			if s != seq+1 {
				t.Fatalf("%s seq %v after %v", event.Type, s, seq)
			}
			seq = s
			critical = append(critical, event.Type)
		}
	}
	want := []string{"turn_start", "round_ready", "tool_call_executing", "tool_call_result", "round_complete", "round_ready", "round_complete", "turn_end"}
	if strings.Join(critical, ",") != strings.Join(want, ",") || events[len(events)-1].Type != "done" {
		t.Fatalf("critical lifecycle = %v, want %v then done", critical, want)
	}

	start := events[0].Data
	origin := start["origin"].(map[string]any)
	if start["alias"] != "qwen" || origin["alias"] != "qwen" || origin["identity"].(map[string]any)["protocol"] != "chat-completions" {
		t.Fatalf("turn_start lacks the resolved origin: %v", start)
	}
	var ready map[string]any
	for _, event := range events {
		if event.Type == "round_ready" {
			ready = event.Data
			break
		}
	}
	assistant := ready["assistant"].(map[string]any)
	if ready["round_id"] != "t1-r1" || assistant["round_id"] != "t1-r1" || assistant["turn_id"] != "t1" || assistant["origin"] == nil || ready["finish"] != "tool_calls" {
		t.Fatalf("round_ready does not carry the finalized round: %v", ready)
	}
	for _, event := range events {
		if event.Type != "round_complete" || event.Data["round_id"] != "t1-r1" {
			continue
		}
		tool := event.Data["tool_messages"].([]any)[0].(map[string]any)
		if tool["turn_id"] != "t1" || tool["round_id"] != "t1-r1" || tool["content"] != "42" {
			t.Fatalf("tool message is not bound: %v", tool)
		}
	}
	end := events[len(events)-2].Data
	if end["outcome"] != "completed" || end["execution"] != "known" {
		t.Fatalf("turn_end = %v", end)
	}
	// the second provider request carried the round's history without
	// pane's recovery metadata.
	second := h.chat.requests()[1]
	if strings.Contains(second, "turn_id") || strings.Contains(second, "round_id") || strings.Contains(second, "origin") {
		t.Fatalf("resent history carries pane metadata: %s", second)
	}
}

func TestTruncatedProviderNeverReachesExecutorThroughAPI(t *testing.T) {
	h := newTurnHarness(t)
	h.chat.script = []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"up\",\"type\":\"function\",\"function\":{\"name\":\"write_note\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n",
	}
	_, events := h.post(t, freshTurn("qwen", "write"))
	if h.tools.count() != 0 {
		t.Fatal("a truncated round reached the executor")
	}
	for _, event := range events {
		if event.Type == "round_ready" || event.Type == "tool_call_executing" || event.Type == "done" {
			t.Fatalf("truncated round produced %s", event.Type)
		}
	}
	end := events[len(events)-1]
	if end.Type != "turn_end" || end.Data["outcome"] != "failed" || end.Data["execution"] != "none" || end.Data["error_code"] != "truncated" {
		t.Fatalf("turn_end = %v", end)
	}
}

func TestChatNeverReadsTheSessionStore(t *testing.T) {
	h := newTurnHarness(t)
	dir := filepath.Join(t.TempDir(), "sessions")
	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	h.api.sessions = store
	// the store's directory is gone: any read or write would fail.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	recorder, events := h.post(t, freshTurn("qwen", "hello"))
	if recorder.Code != http.StatusOK || events[len(events)-1].Type != "done" {
		t.Fatalf("chat depended on the store: status=%d events=%v", recorder.Code, eventTypes(events))
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("chat touched the store directory: %v", err)
	}
}

func TestChatContractBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, body, code string
		status           int
	}{
		{"turn id without recovery", `{"model":"qwen","turn_id":"t1","messages":[{"role":"user","content":"x","turn_id":"t1"}]}`, "invalid_recovery", http.StatusBadRequest},
		{"recovery without turn id", `{"model":"qwen","recovery":{"v":1,"turns":[]},"messages":[{"role":"user","content":"x"}]}`, "invalid_recovery", http.StatusBadRequest},
		{"recovery without turns", `{"model":"qwen","turn_id":"t1","recovery":{"v":1},"messages":[{"role":"user","content":"x","turn_id":"t1"}]}`, "invalid_recovery", http.StatusBadRequest},
		{"malformed record", `{"model":"qwen","turn_id":"t1","recovery":{"v":1,"turns":[{"id":7}]},"messages":[{"role":"user","content":"x","turn_id":"t1"}]}`, "invalid_recovery", http.StatusBadRequest},
		{"unknown model", `{"model":"nope","messages":[]}`, "unknown_model", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTurnHarness(t)
			recorder, _ := h.post(t, tt.body)
			if recorder.Code != tt.status || chatErrorCode(t, recorder) != tt.code || len(h.chat.requests()) != 0 {
				t.Fatalf("status=%d body=%q requests=%d", recorder.Code, recorder.Body.String(), len(h.chat.requests()))
			}
		})
	}
}

func TestLegacyRequestGetsServerTurnID(t *testing.T) {
	h := newTurnHarness(t)
	recorder, events := h.post(t, `{"model":"qwen","messages":[{"role":"user","content":"hello"}]}`)
	if recorder.Code != http.StatusOK || len(h.chat.requests()) != 1 {
		t.Fatalf("legacy request failed: %d", recorder.Code)
	}
	if id, _ := events[0].Data["turn_id"].(string); events[0].Type != "turn_start" || !strings.HasPrefix(id, "srv_") {
		t.Fatalf("legacy turn id = %v", events[0].Data)
	}
}

func TestChatBodyCapAppliesBeforeDecoding(t *testing.T) {
	h := newTurnHarness(t)
	body := `{"model":"qwen","messages":[{"role":"user","content":"` + strings.Repeat("x", session.MaxDocumentSize) + `"}]}`
	recorder, _ := h.post(t, body)
	if recorder.Code != http.StatusRequestEntityTooLarge || chatErrorCode(t, recorder) != "request_too_large" || len(h.chat.requests()) != 0 {
		t.Fatalf("status=%d requests=%d", recorder.Code, len(h.chat.requests()))
	}
}

// indices refer to the submitted array before system-prompt normalization:
// a system message in the submitted history shifts nothing the validator
// sees, even though the backend drops it and prepends its own prompt.
func TestRecoveryIndicesPrecedeSystemNormalization(t *testing.T) {
	history := `{"role":"system","content":"stale"},` +
		`{"role":"user","content":"hi","turn_id":"t0"},` +
		`{"role":"assistant","content":"hello","turn_id":"t0","round_id":"t0-r1"},` +
		`{"role":"user","content":"next","turn_id":"t1"}`
	record := func(user, round int) string {
		return `{"id":"t0","v":1,"model_alias":"qwen","user_message_index":` + itoa(user) + `,"state":"completed","last_seq":4,` +
			`"terminal":{"outcome":"completed","execution":"none"},"rounds":[{"round_id":"t0-r1","message_index":` + itoa(round) + `,"calls":[],"committed":true}]}`
	}
	for _, tt := range []struct {
		name        string
		user, round int
		status      int
	}{
		{"submitted indices", 1, 2, http.StatusOK},
		{"normalized indices", 0, 1, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newTurnHarness(t)
			body := `{"model":"qwen","system_prompt_mode":"custom","system_prompt":"fresh","turn_id":"t1","recovery":{"v":1,"turns":[` + record(tt.user, tt.round) + `]},"messages":[` + history + `]}`
			recorder, _ := h.post(t, body)
			if recorder.Code != tt.status {
				t.Fatalf("status=%d body=%q", recorder.Code, recorder.Body.String())
			}
			if tt.status == http.StatusOK {
				sent := h.chat.requests()[0]
				if strings.Contains(sent, "stale") || !strings.Contains(sent, "fresh") {
					t.Fatalf("system normalization changed: %s", sent)
				}
			}
		})
	}
}

func itoa(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestSubscriptionAvailabilityRefusesBeforeGeneration(t *testing.T) {
	h := newTurnHarness(t)
	h.subscription.set("", "", "")
	recorder, _ := h.post(t, freshTurn("sol", "hello"))
	if recorder.Code != http.StatusServiceUnavailable || chatErrorCode(t, recorder) != "login_required" || len(h.codex.requests()) != 0 {
		t.Fatalf("signed-out alias: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "pane auth login openai") {
		t.Fatalf("no cli guidance: %s", recorder.Body.String())
	}
	h.api.SetSubscriptionAuth(nil, os.ErrPermission)
	recorder, _ = h.post(t, freshTurn("sol", "hello"))
	if recorder.Code != http.StatusServiceUnavailable || chatErrorCode(t, recorder) != "auth_error" || len(h.codex.requests()) != 0 {
		t.Fatalf("unreadable store: status=%d body=%q", recorder.Code, recorder.Body.String())
	}
	// the other connections are unaffected.
	recorder, _ = h.post(t, freshTurn("qwen", "hello"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("generic alias blocked by subscription state: %d", recorder.Code)
	}
}

// the submitted alias fixes its account binding and effort preset for every
// round of the turn, including tool rounds.
func TestSubscriptionTurnKeepsAccountAndEffortAcrossToolRounds(t *testing.T) {
	for _, tt := range []struct{ alias, effort string }{{"sol", "low"}, {"sol@high", "high"}} {
		t.Run(tt.alias, func(t *testing.T) {
			h := newTurnHarness(t)
			h.codex.script = []string{codexToolCall("add", `{"a":1}`), codexText("done")}
			recorder, events := h.post(t, freshTurn(tt.alias, "add"))
			requests := h.codex.requests()
			if recorder.Code != http.StatusOK || len(requests) != 2 || h.tools.count() != 1 {
				t.Fatalf("status=%d requests=%d executions=%d", recorder.Code, len(requests), h.tools.count())
			}
			for i, request := range requests {
				var body map[string]any
				if err := json.Unmarshal([]byte(request), &body); err != nil {
					t.Fatal(err)
				}
				if body["reasoning"].(map[string]any)["effort"] != tt.effort {
					t.Fatalf("request %d effort = %v", i, body["reasoning"])
				}
				if h.codex.headers[i].Get("ChatGPT-Account-ID") != "acct-pane-test-a" {
					t.Fatalf("request %d used another account", i)
				}
			}
			identity := events[0].Data["origin"].(map[string]any)["identity"].(map[string]any)
			if identity["account_scope"] != "chatgpt:ea1e30f202148af5945f8c85" || strings.Contains(recorder.Body.String(), "acct-pane-test-a") {
				t.Fatalf("turn origin identity = %v", identity)
			}
		})
	}
}

func TestModelsReportLocalAvailabilityWithoutProviderTraffic(t *testing.T) {
	h := newTurnHarness(t)
	list := func() map[string]map[string]any {
		recorder := httptest.NewRecorder()
		h.api.handleModels(recorder, httptest.NewRequest(http.MethodGet, "/api/models", nil))
		var body struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		entries := make(map[string]map[string]any)
		for _, entry := range body.Data {
			entries[entry["id"].(string)] = entry
		}
		return entries
	}

	entries := list()
	if entries["qwen"]["auth_state"] != "not_required" || entries["sol"]["auth_state"] != "credential_available" || entries["sol"]["provider"] != "openai-codex" {
		t.Fatalf("availability = %v", entries)
	}
	if len(h.codex.requests()) != 0 || len(h.chat.requests()) != 0 || h.subscription.accesses != 0 {
		t.Fatal("listing reached a provider or requested access")
	}

	// a rejected credential surfaces as the alias's last error, without
	// disabling it, until the credential changes.
	h.codex.status = http.StatusUnauthorized
	h.post(t, freshTurn("sol", "hello"))
	entries = list()
	lastError, _ := entries["sol"]["last_error"].(map[string]any)
	if lastError == nil || lastError["code"] != "auth" || entries["sol"]["auth_state"] != "credential_available" || entries["qwen"]["last_error"] != nil {
		t.Fatalf("auth failure not reported: %v", entries)
	}
	h.subscription.set("acct-pane-test-a", "token-b", "2")
	if entries = list(); entries["sol"]["last_error"] != nil {
		t.Fatalf("relogin did not retire the auth failure: %v", entries["sol"])
	}

	h.subscription.set("", "", "")
	if entries = list(); entries["sol"]["auth_state"] != "login_required" {
		t.Fatalf("logout not reflected: %v", entries["sol"])
	}
	h.subscription.statusErr = os.ErrInvalid
	if entries = list(); entries["sol"]["auth_state"] != "error" || entries["qwen"]["auth_state"] != "not_required" {
		t.Fatalf("corrupt credentials not isolated: %v", entries)
	}
}

// qwen's active-round reasoning replays only inside the Go request: the next
// tool round sees it, while lifecycle events -- and therefore the browser's
// saved record -- never do, and later requests cannot restore it from saved
// display thinking.
func TestQwenCanaryStaysRequestLocalThroughAPI(t *testing.T) {
	h := newTurnHarness(t)
	h.chat.script = []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"ACTIVE_CANARY\",\"tool_calls\":[{\"index\":0,\"id\":\"up\",\"type\":\"function\",\"function\":{\"name\":\"add\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
		chatText("4"),
	}
	_, events := h.post(t, freshTurn("qwen@ninfer", "add"))
	requests := h.chat.requests()
	if len(requests) != 2 || !strings.Contains(requests[1], "ACTIVE_CANARY") {
		t.Fatalf("active tool round lost its reasoning: %v", requests)
	}
	var display bool
	for _, event := range events {
		switch event.Type {
		case "thinking_delta":
			display = display || strings.Contains(event.Raw, "ACTIVE_CANARY")
		case "round_ready", "round_complete", "turn_end":
			if strings.Contains(event.Raw, "ACTIVE_CANARY") {
				t.Fatalf("%s carries request-local reasoning: %s", event.Type, event.Raw)
			}
		}
	}
	if !display {
		t.Fatal("visible thinking was not displayed")
	}

	next := `{"model":"qwen@ninfer","messages":[{"role":"user","content":"add"},{"role":"assistant","content":"4","thinking":"ACTIVE_CANARY","reasoning_content":"ACTIVE_CANARY"},{"role":"user","content":"again"}]}`
	h.post(t, next)
	if last := h.chat.requests()[2]; strings.Contains(last, "ACTIVE_CANARY") {
		t.Fatalf("saved display thinking became replay input: %s", last)
	}
}
