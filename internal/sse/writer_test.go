package sse

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelquigley/df/dd"
)

func TestWriterUsesDdFieldsOnOneDataLine(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, err := NewWriter(recorder)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Send("tool_call_result", ToolCallResultData{
		Index: 2, ID: "pane-call", Name: "read", Status: "complete",
		Content: "a\nb", DurationMS: 17, ExecutionState: "result_received",
	}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(recorder.Body.String(), "\n\n"), "\n")
	if len(lines) != 2 || lines[0] != "event: tool_call_result" || !strings.HasPrefix(lines[1], "data: ") {
		t.Fatalf("event is not one data line: %q", recorder.Body.String())
	}
	payload, err := dd.DecodeStrictJSON([]byte(strings.TrimPrefix(lines[1], "data: ")))
	if err != nil {
		t.Fatal(err)
	}
	if payload["execution_state"] != "result_received" || payload["duration_ms"] == nil || payload["content"] != "a\nb" {
		t.Fatalf("wrong event fields: %#v", payload)
	}
	if _, present := payload["error_code"]; present {
		t.Fatalf("empty optional error_code emitted: %#v", payload)
	}
}
