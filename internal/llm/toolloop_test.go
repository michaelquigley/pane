package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/michaelquigley/pane/internal/sse"
)

type testExecutor struct {
	result   string
	duration time.Duration
	err      error
	approve  bool
}

func (t testExecutor) CallTool(_ context.Context, _ string, _ map[string]any) (string, time.Duration, error) {
	return t.result, t.duration, t.err
}

func (t testExecutor) NeedsApproval(string) bool {
	return t.approve
}

type recordedEvent struct {
	Type string
	Data json.RawMessage
}

type testApprovalRegistry struct {
	ch chan bool
}

func (r *testApprovalRegistry) Register(string) <-chan bool {
	return r.ch
}

func (r *testApprovalRegistry) Unregister(string) {}

func TestRunToolLoopEmitsRoundCompletePerIteration(t *testing.T) {
	t.Parallel()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		switch requestCount {
		case 1:
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						Content: StringContent("let me check"),
					},
				}},
			})
			idx := 0
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						ToolCalls: []ToolCall{{
							ID:    "call_1",
							Type:  "function",
							Index: &idx,
							Function: ToolCallFunction{
								Name:      "filesystem_read_file",
								Arguments: `{"path":"README.md"}`,
							},
						}},
					},
				}},
			})
		case 2:
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-2",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						Content: StringContent("done"),
					},
				}},
			})
		default:
			t.Fatalf("unexpected chat completion request %d", requestCount)
		}

		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model", "")
	recorder := httptest.NewRecorder()
	sw, err := sse.NewWriter(recorder)
	if err != nil {
		t.Fatalf("creating SSE writer: %v", err)
	}

	err = RunToolLoop(
		context.Background(),
		client,
		[]Message{{Role: "user", Content: StringContent("read the README")}},
		"test-model",
		[]Tool{{
			Type: "function",
			Function: &FunctionDef{
				Name:       "filesystem_read_file",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		testExecutor{result: "README contents", duration: 12 * time.Millisecond},
		sw,
		nil,
	)
	if err != nil {
		t.Fatalf("running tool loop: %v", err)
	}

	events := parseRecordedEvents(t, recorder.Body.String())
	assertEventTypes(t, events,
		"delta",
		"tool_call_start",
		"tool_call_args",
		"tool_call_executing",
		"tool_call_result",
		"round_complete",
		"delta",
		"round_complete",
		"done",
	)

	var start sse.ToolCallStartData
	if err := json.Unmarshal(events[1].Data, &start); err != nil {
		t.Fatalf("unmarshaling tool_call_start: %v", err)
	}
	if start.Index != 0 {
		t.Fatalf("expected tool_call_start index 0, got %d", start.Index)
	}

	var firstRound roundCompleteData
	if err := json.Unmarshal(events[5].Data, &firstRound); err != nil {
		t.Fatalf("unmarshaling first round_complete: %v", err)
	}
	if firstRound.Assistant.Role != "assistant" {
		t.Fatalf("expected assistant role, got %q", firstRound.Assistant.Role)
	}
	if firstRound.Assistant.Content == nil || *firstRound.Assistant.Content != "let me check" {
		t.Fatalf("unexpected assistant content: %#v", firstRound.Assistant.Content)
	}
	if len(firstRound.Assistant.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(firstRound.Assistant.ToolCalls))
	}
	if len(firstRound.ToolMessages) != 1 {
		t.Fatalf("expected 1 tool message, got %d", len(firstRound.ToolMessages))
	}

	var firstResult sse.ToolCallResultData
	if err := json.Unmarshal(events[4].Data, &firstResult); err != nil {
		t.Fatalf("unmarshaling tool_call_result: %v", err)
	}
	if firstResult.Status != toolCallStatusComplete || firstResult.ErrorCode != "" {
		t.Fatalf("unexpected tool_call_result outcome: %#v", firstResult)
	}

	if firstRound.ToolMessages[0].Role != "tool" {
		t.Fatalf("expected tool role, got %q", firstRound.ToolMessages[0].Role)
	}
	if firstRound.ToolMessages[0].Content == nil || *firstRound.ToolMessages[0].Content != "README contents" {
		t.Fatalf("unexpected tool content: %#v", firstRound.ToolMessages[0].Content)
	}

	var secondRound roundCompleteData
	if err := json.Unmarshal(events[7].Data, &secondRound); err != nil {
		t.Fatalf("unmarshaling second round_complete: %v", err)
	}
	if secondRound.Assistant.Content == nil || *secondRound.Assistant.Content != "done" {
		t.Fatalf("unexpected final assistant content: %#v", secondRound.Assistant.Content)
	}
	if len(secondRound.ToolMessages) != 0 {
		t.Fatalf("expected no tool messages in final round, got %d", len(secondRound.ToolMessages))
	}
}

func TestRunToolLoopEmitsIndexedToolEventsForFragmentedMetadata(t *testing.T) {
	t.Parallel()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		switch requestCount {
		case 1:
			idx := 0
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						ToolCalls: []ToolCall{{
							Index: &idx,
							Function: ToolCallFunction{
								Arguments: `{"path":`,
							},
						}},
					},
				}},
			})
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						ToolCalls: []ToolCall{{
							Index: &idx,
							Function: ToolCallFunction{
								Name: "filesystem_read_file",
							},
						}},
					},
				}},
			})
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						ToolCalls: []ToolCall{{
							ID:    "call_fragmented",
							Index: &idx,
							Function: ToolCallFunction{
								Arguments: `"README.md"}`,
							},
						}},
					},
				}},
			})
		case 2:
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-2",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						Content: StringContent("done"),
					},
				}},
			})
		default:
			t.Fatalf("unexpected chat completion request %d", requestCount)
		}

		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model", "")
	recorder := httptest.NewRecorder()
	sw, err := sse.NewWriter(recorder)
	if err != nil {
		t.Fatalf("creating SSE writer: %v", err)
	}

	err = RunToolLoop(
		context.Background(),
		client,
		[]Message{{Role: "user", Content: StringContent("read the README")}},
		"test-model",
		[]Tool{{
			Type: "function",
			Function: &FunctionDef{
				Name:       "filesystem_read_file",
				Parameters: json.RawMessage(`{"type":"object"}`),
			},
		}},
		testExecutor{result: "README contents", duration: 5 * time.Millisecond},
		sw,
		nil,
	)
	if err != nil {
		t.Fatalf("running tool loop: %v", err)
	}

	events := parseRecordedEvents(t, recorder.Body.String())
	assertEventTypes(t, events,
		"tool_call_start",
		"tool_call_args",
		"tool_call_start",
		"tool_call_start",
		"tool_call_args",
		"tool_call_executing",
		"tool_call_result",
		"round_complete",
		"delta",
		"round_complete",
		"done",
	)

	var start0 sse.ToolCallStartData
	if err := json.Unmarshal(events[0].Data, &start0); err != nil {
		t.Fatalf("unmarshaling initial tool_call_start: %v", err)
	}
	if start0.Index != 0 || start0.ID != "" || start0.Name != "" {
		t.Fatalf("unexpected initial tool_call_start payload: %#v", start0)
	}

	var args0 sse.ToolCallArgsData
	if err := json.Unmarshal(events[1].Data, &args0); err != nil {
		t.Fatalf("unmarshaling initial tool_call_args: %v", err)
	}
	if args0.Index != 0 || args0.ID != "" {
		t.Fatalf("unexpected initial tool_call_args payload: %#v", args0)
	}

	var start1 sse.ToolCallStartData
	if err := json.Unmarshal(events[2].Data, &start1); err != nil {
		t.Fatalf("unmarshaling name update tool_call_start: %v", err)
	}
	if start1.Index != 0 || start1.Name != "filesystem_read_file" || start1.ID != "" {
		t.Fatalf("unexpected name update payload: %#v", start1)
	}

	var start2 sse.ToolCallStartData
	if err := json.Unmarshal(events[3].Data, &start2); err != nil {
		t.Fatalf("unmarshaling id update tool_call_start: %v", err)
	}
	if start2.Index != 0 || start2.Name != "filesystem_read_file" || start2.ID != "call_fragmented" {
		t.Fatalf("unexpected id update payload: %#v", start2)
	}

	var result sse.ToolCallResultData
	if err := json.Unmarshal(events[6].Data, &result); err != nil {
		t.Fatalf("unmarshaling tool_call_result: %v", err)
	}
	if result.Index != 0 || result.ID != "call_fragmented" || result.Name != "filesystem_read_file" {
		t.Fatalf("unexpected tool_call_result payload: %#v", result)
	}
	if result.Status != toolCallStatusComplete || result.ErrorCode != "" {
		t.Fatalf("unexpected fragmented tool_call_result outcome: %#v", result)
	}
}

func TestRunToolLoopEmitsIndexedToolEventsForInterleavedToolCalls(t *testing.T) {
	t.Parallel()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "text/event-stream")

		switch requestCount {
		case 1:
			idx0 := 0
			idx1 := 1
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						ToolCalls: []ToolCall{
							{
								ID:    "call_0",
								Index: &idx0,
								Function: ToolCallFunction{
									Name:      "first_tool",
									Arguments: `{"step":"one"}`,
								},
							},
							{
								Index: &idx1,
								Function: ToolCallFunction{
									Arguments: `{"step":"`,
								},
							},
						},
					},
				}},
			})
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-1",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						ToolCalls: []ToolCall{{
							ID:    "call_1",
							Index: &idx1,
							Function: ToolCallFunction{
								Name:      "second_tool",
								Arguments: `two"}`,
							},
						}},
					},
				}},
			})
		case 2:
			writeStreamChunk(t, w, StreamChunk{
				ID: "chat-2",
				Choices: []Choice{{
					Index: 0,
					Delta: Delta{
						Content: StringContent("done"),
					},
				}},
			})
		default:
			t.Fatalf("unexpected chat completion request %d", requestCount)
		}

		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model", "")
	recorder := httptest.NewRecorder()
	sw, err := sse.NewWriter(recorder)
	if err != nil {
		t.Fatalf("creating SSE writer: %v", err)
	}

	err = RunToolLoop(
		context.Background(),
		client,
		[]Message{{Role: "user", Content: StringContent("run both tools")}},
		"test-model",
		[]Tool{
			{
				Type: "function",
				Function: &FunctionDef{
					Name:       "first_tool",
					Parameters: json.RawMessage(`{"type":"object"}`),
				},
			},
			{
				Type: "function",
				Function: &FunctionDef{
					Name:       "second_tool",
					Parameters: json.RawMessage(`{"type":"object"}`),
				},
			},
		},
		testExecutor{result: "ok", duration: 3 * time.Millisecond},
		sw,
		nil,
	)
	if err != nil {
		t.Fatalf("running tool loop: %v", err)
	}

	events := parseRecordedEvents(t, recorder.Body.String())

	seenStart := map[int]bool{}
	seenArgs := map[int]bool{}
	seenResult := map[int]bool{}
	for _, event := range events {
		switch event.Type {
		case "tool_call_start":
			var payload sse.ToolCallStartData
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatalf("unmarshaling tool_call_start: %v", err)
			}
			seenStart[payload.Index] = true
		case "tool_call_args":
			var payload sse.ToolCallArgsData
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatalf("unmarshaling tool_call_args: %v", err)
			}
			seenArgs[payload.Index] = true
		case "tool_call_result":
			var payload sse.ToolCallResultData
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				t.Fatalf("unmarshaling tool_call_result: %v", err)
			}
			seenResult[payload.Index] = true
		}
	}

	for _, idx := range []int{0, 1} {
		if !seenStart[idx] {
			t.Fatalf("missing tool_call_start for index %d", idx)
		}
		if !seenArgs[idx] {
			t.Fatalf("missing tool_call_args for index %d", idx)
		}
		if !seenResult[idx] {
			t.Fatalf("missing tool_call_result for index %d", idx)
		}
	}
}

func TestRunToolLoopEmitsRoundCompleteForNoToolResponses(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamChunk(t, w, StreamChunk{
			ID: "chat-1",
			Choices: []Choice{{
				Index: 0,
				Delta: Delta{
					Content: StringContent("hello"),
				},
			}},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model", "")
	recorder := httptest.NewRecorder()
	sw, err := sse.NewWriter(recorder)
	if err != nil {
		t.Fatalf("creating SSE writer: %v", err)
	}

	err = RunToolLoop(
		context.Background(),
		client,
		[]Message{{Role: "user", Content: StringContent("hello")}},
		"test-model",
		nil,
		testExecutor{},
		sw,
		nil,
	)
	if err != nil {
		t.Fatalf("running tool loop: %v", err)
	}

	events := parseRecordedEvents(t, recorder.Body.String())
	assertEventTypes(t, events, "delta", "round_complete", "done")

	var round roundCompleteData
	if err := json.Unmarshal(events[1].Data, &round); err != nil {
		t.Fatalf("unmarshaling round_complete: %v", err)
	}
	if round.Assistant.Content == nil || *round.Assistant.Content != "hello" {
		t.Fatalf("unexpected assistant content: %#v", round.Assistant.Content)
	}
	if len(round.ToolMessages) != 0 {
		t.Fatalf("expected no tool messages, got %d", len(round.ToolMessages))
	}
}

func TestExecuteSingleToolReturnsStructuredOutcomes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		ctx        func() context.Context
		pending    *pendingToolCall
		executor   ToolExecutor
		approvals  ApprovalRegistry
		wantStatus string
		wantCode   string
		wantText   string
	}{
		{
			name: "success",
			ctx:  context.Background,
			pending: &pendingToolCall{
				ID:        "call_success",
				Name:      "filesystem_read_file",
				Arguments: `{"path":"README.md"}`,
				Index:     0,
			},
			executor:   testExecutor{result: "README contents", duration: 7 * time.Millisecond},
			wantStatus: toolCallStatusComplete,
			wantText:   "README contents",
		},
		{
			name: "denied",
			ctx:  context.Background,
			pending: &pendingToolCall{
				ID:        "call_denied",
				Name:      "filesystem_read_file",
				Arguments: `{"path":"README.md"}`,
				Index:     0,
			},
			executor:   testExecutor{approve: true},
			approvals:  &testApprovalRegistry{ch: bufferedApproval(false)},
			wantStatus: toolCallStatusError,
			wantCode:   toolCallErrorDenied,
			wantText:   "Tool call denied by user",
		},
		{
			name: "cancelled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			pending: &pendingToolCall{
				ID:        "call_cancelled",
				Name:      "filesystem_read_file",
				Arguments: `{"path":"README.md"}`,
				Index:     0,
			},
			executor:   testExecutor{approve: true},
			approvals:  &testApprovalRegistry{ch: make(chan bool)},
			wantStatus: toolCallStatusError,
			wantCode:   toolCallErrorCancelled,
			wantText:   "Request cancelled",
		},
		{
			name: "malformed arguments",
			ctx:  context.Background,
			pending: &pendingToolCall{
				ID:        "call_bad_args",
				Name:      "filesystem_read_file",
				Arguments: `{"path":`,
				Index:     0,
			},
			executor:   testExecutor{},
			wantStatus: toolCallStatusError,
			wantCode:   toolCallErrorMalformedArguments,
			wantText:   "Error: malformed arguments:",
		},
		{
			name: "execution error",
			ctx:  context.Background,
			pending: &pendingToolCall{
				ID:        "call_exec_error",
				Name:      "filesystem_read_file",
				Arguments: `{"path":"README.md"}`,
				Index:     0,
			},
			executor:   testExecutor{duration: 11 * time.Millisecond, err: errors.New("boom")},
			wantStatus: toolCallStatusError,
			wantCode:   toolCallErrorExecution,
			wantText:   "Error: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sw := newTestSSEWriter(t)
			result := executeSingleTool(tt.ctx(), tt.pending, tt.executor, sw, tt.approvals)

			if result.Status != tt.wantStatus {
				t.Fatalf("expected status %q, got %q", tt.wantStatus, result.Status)
			}
			if result.ErrorCode != tt.wantCode {
				t.Fatalf("expected error code %q, got %q", tt.wantCode, result.ErrorCode)
			}
			if !strings.Contains(result.Content, tt.wantText) {
				t.Fatalf("expected result content %q to contain %q", result.Content, tt.wantText)
			}
		})
	}
}

func writeStreamChunk(t *testing.T, w http.ResponseWriter, chunk StreamChunk) {
	t.Helper()

	data, err := json.Marshal(chunk)
	if err != nil {
		t.Fatalf("marshaling chunk: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func parseRecordedEvents(t *testing.T, output string) []recordedEvent {
	t.Helper()

	blocks := strings.Split(output, "\n\n")
	events := make([]recordedEvent, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var event recordedEvent
		for _, line := range strings.Split(block, "\n") {
			switch {
			case strings.HasPrefix(line, "event: "):
				event.Type = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				event.Data = json.RawMessage(strings.TrimPrefix(line, "data: "))
			}
		}
		events = append(events, event)
	}
	return events
}

func assertEventTypes(t *testing.T, events []recordedEvent, expected ...string) {
	t.Helper()

	if len(events) != len(expected) {
		t.Fatalf("expected %d events, got %d", len(expected), len(events))
	}
	for idx, want := range expected {
		if events[idx].Type != want {
			t.Fatalf("event %d: expected %q, got %q", idx, want, events[idx].Type)
		}
	}
}

func bufferedApproval(approved bool) chan bool {
	ch := make(chan bool, 1)
	ch <- approved
	return ch
}

func newTestSSEWriter(t *testing.T) *sse.Writer {
	t.Helper()

	sw, err := sse.NewWriter(httptest.NewRecorder())
	if err != nil {
		t.Fatalf("creating SSE writer: %v", err)
	}
	return sw
}
