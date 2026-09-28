package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recoveryFixture struct {
	Expect string          `json:"expect"`
	Body   json.RawMessage `json:"body"`
}

// paneMetadata names every pane-only field that must never reach a
// provider request.
var paneMetadata = []string{"turn_id", "round_id", "recovery_placeholder", "\"recovery\"", "\"origin\"", "\"continuation\"", "partial_text", "thinking", "tool_call_results"}

// TestRecoveryFixturesThroughHandler replays the request bodies the
// browser's serializer produced (ui/src/lib/turnRecord.test.ts) through the
// real handler, once for a chat-completions alias and once for a
// subscription alias. accepted projections reach each provider exactly once
// and carry only history and attribution text; rejected ones reach neither
// a provider nor the executor.
func TestRecoveryFixturesThroughHandler(t *testing.T) {
	paths, err := filepath.Glob("testdata/recovery/*.json")
	if err != nil || len(paths) < 30 {
		t.Fatalf("recovery fixtures missing (%d found): %v", len(paths), err)
	}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture recoveryFixture
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(fixture.Body, &body); err != nil {
				t.Fatal(err)
			}
			for _, model := range []string{"qwen", "sol"} {
				h := newTurnHarness(t)
				body["model"] = model
				encoded, _ := json.Marshal(body)
				recorder, events := h.post(t, string(encoded))
				provider := h.chat
				if model == "sol" {
					provider = h.codex
				}
				requests := provider.requests()
				if h.tools.count() != 0 {
					t.Fatalf("%s: projection executed a tool", model)
				}
				if fixture.Expect != "accepted" {
					if len(requests) != 0 || len(h.chat.requests())+len(h.codex.requests()) != 0 {
						t.Fatalf("%s: rejected projection reached a provider", model)
					}
					want := http.StatusBadRequest
					if fixture.Expect == "recovery_required" {
						want = http.StatusConflict
					}
					if recorder.Code != want || chatErrorCode(t, recorder) != fixture.Expect {
						t.Fatalf("%s: status=%d body=%q, want %d '%s'", model, recorder.Code, recorder.Body.String(), want, fixture.Expect)
					}
					return
				}
				if recorder.Code != http.StatusOK || len(requests) != 1 {
					t.Fatalf("%s: accepted projection failed: status=%d requests=%d body=%q", model, recorder.Code, len(requests), recorder.Body.String())
				}
				for _, field := range paneMetadata {
					if strings.Contains(requests[0], field) {
						t.Fatalf("%s: provider request carries pane metadata %s: %s", model, field, requests[0])
					}
				}
				for _, message := range body["messages"].([]any) {
					fields := message.(map[string]any)
					if fields["recovery_placeholder"] == nil {
						continue
					}
					content, _ := json.Marshal(fields["content"])
					if !strings.Contains(requests[0], strings.Trim(string(content), `"`)) {
						t.Fatalf("%s: attribution text did not reach the provider", model)
					}
				}
				if types := eventTypes(events); types[0] != "turn_start" || types[len(types)-1] != "done" {
					t.Fatalf("%s: unexpected lifecycle %v", model, types)
				}
			}
		})
	}
}
