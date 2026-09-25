package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/sse"
)

type sequentialExecutor struct{ calls int }

func (e *sequentialExecutor) NeedsApproval(string) bool { return false }

func (e *sequentialExecutor) CallTool(context.Context, string, map[string]any) ToolExecution {
	e.calls++
	if e.calls == 1 {
		return ToolExecution{Dispatch: ResultReceived, Content: "first result"}
	}
	return ToolExecution{Dispatch: UnknownDispatch, Err: errors.New("connection lost")}
}

func TestUnknownSecondOutcomeRetainsReceivedFirstResult(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"read","arguments":"{}"}},{"index":1,"id":"b","type":"function","function":{"name":"read","arguments":"{}"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	writer, err := sse.NewWriter(recorder)
	if err != nil {
		t.Fatal(err)
	}
	executor := &sequentialExecutor{}
	err = runTestToolLoop(context.Background(), NewClient(server.URL, "test", "", false),
		[]Message{{Role: "user", Content: StringContent("read twice")}}, "test", 0,
		[]Tool{{Type: "function", Function: &FunctionDef{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}}, executor, writer, nil)
	if err == nil || executor.calls != 2 || requests != 1 {
		t.Fatalf("expected one model round, two distinct tool calls, and unknown outcome; got err=%v calls=%d requests=%d", err, executor.calls, requests)
	}
	events := parseRecordedEvents(t, recorder.Body.String())
	var results []sse.ToolCallResultData
	for _, event := range events {
		if event.Type == "round_complete" || event.Type == "done" {
			t.Fatalf("interrupted round was committed as complete: %s", event.Type)
		}
		if event.Type == "tool_call_result" {
			var result sse.ToolCallResultData
			if err := dd.BindJSON(&result, event.Data); err != nil {
				t.Fatal(err)
			}
			results = append(results, result)
		}
	}
	if len(results) != 2 || results[0].ExecutionState != string(ResultReceived) || results[0].Content != "first result" ||
		results[1].ExecutionState != string(UnknownDispatch) {
		t.Fatalf("outcome events lost dispatch evidence: %#v", results)
	}
	type outcomeDocument struct {
		Results []sse.ToolCallResultData
	}
	data, err := dd.UnbindJSON(outcomeDocument{Results: results})
	if err != nil {
		t.Fatal(err)
	}
	var restored outcomeDocument
	if err := dd.BindJSON(&restored, data); err != nil {
		t.Fatal(err)
	}
	if restored.Results[0].ExecutionState != string(ResultReceived) || restored.Results[1].ExecutionState != string(UnknownDispatch) {
		t.Fatalf("serialized evidence changed: %#v", restored)
	}
}
