package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	payload, err := json.Marshal(data)
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

// Event data types for the pane SSE protocol.

type DeltaData struct {
	Content string `json:"content"`
}

type ErrorData struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

type ToolCallStartData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ToolCallArgsData struct {
	ID               string `json:"id"`
	ArgumentsPartial string `json:"arguments_partial"`
}

type ToolCallExecutingData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ToolCallApproveData struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCallResultData struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	DurationMS int64  `json:"duration_ms"`
}
