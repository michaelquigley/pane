package sse

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/michaelquigley/df/dd"
)

type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func NewWriter(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support flushing")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()
	return &Writer{w: w, flusher: flusher}, nil
}

func (s *Writer) Send(eventType string, data any) error {
	unbound, err := dd.Unbind(data)
	if err != nil {
		return fmt.Errorf("unbinding SSE data: %w", err)
	}
	payload, err := json.Marshal(unbound)
	if err != nil {
		return fmt.Errorf("marshaling SSE data: %w", err)
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", eventType, payload); err != nil {
		return fmt.Errorf("writing SSE event: %w", err)
	}
	s.flusher.Flush()
	return nil
}

func (s *Writer) SendDone() error {
	if _, err := fmt.Fprint(s.w, "event: done\ndata: {}\n\n"); err != nil {
		return fmt.Errorf("writing SSE done: %w", err)
	}
	s.flusher.Flush()
	return nil
}

// event data types for the pane SSE protocol.

type DeltaData struct {
	Content string `dd:"content"`
}

type ThinkingDeltaData struct {
	Content string `dd:"content"`
}

type UsageData struct {
	PromptTokens     int `dd:"prompt_tokens"`
	CompletionTokens int `dd:"completion_tokens"`
	TotalTokens      int `dd:"total_tokens"`
}

type ErrorData struct {
	Code       string `dd:"code"`
	Message    string `dd:"message"`
	ToolCallID string `dd:"tool_call_id,+omitempty"`
}

type ToolCallStartData struct {
	Index int    `dd:"index"`
	ID    string `dd:"id"`
	Name  string `dd:"name"`
}

type ToolCallArgsData struct {
	Index            int    `dd:"index"`
	ID               string `dd:"id"`
	ArgumentsPartial string `dd:"arguments_partial"`
}

type ToolCallExecutingData struct {
	Index int    `dd:"index"`
	ID    string `dd:"id"`
	Name  string `dd:"name"`
}

type ToolCallApproveData struct {
	Index     int    `dd:"index"`
	ID        string `dd:"id"`
	Name      string `dd:"name"`
	Arguments string `dd:"arguments"`
}

type ToolCallResultData struct {
	Index          int    `dd:"index"`
	ID             string `dd:"id"`
	Name           string `dd:"name"`
	Status         string `dd:"status"`
	ErrorCode      string `dd:"error_code,+omitempty"`
	Content        string `dd:"content"`
	DurationMS     int64  `dd:"duration_ms"`
	ExecutionState string `dd:"execution_state,+omitempty"`
}
