package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/sse"
)

type chatRequest struct {
	Model         string        `json:"model"`
	Messages      []llm.Message `json:"messages"`
	ToolsDisabled []string      `json:"tools_disabled"`
}

func (a *API) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	model := req.Model
	if model == "" {
		model = a.cfg.Model
	}

	// prepend system prompt if configured and not already present
	if a.cfg.System != "" && (len(req.Messages) == 0 || req.Messages[0].Role != "system") {
		systemMsg := llm.Message{
			Role:    "system",
			Content: llm.StringContent(a.cfg.System),
		}
		req.Messages = append([]llm.Message{systemMsg}, req.Messages...)
	}

	sw, err := sse.NewWriter(w)
	if err != nil {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	chatReq := &llm.ChatRequest{
		Model:    model,
		Messages: req.Messages,
	}

	stream, err := a.llm.StreamChat(r.Context(), chatReq)
	if err != nil {
		dl.Errorf("starting chat stream: %v", err)
		code := "upstream_error"
		if strings.Contains(err.Error(), "unreachable") {
			code = "upstream_unreachable"
		}
		_ = sw.Send("error", sse.ErrorData{Code: code, Message: err.Error()})
		return
	}
	defer stream.Close()

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				_ = sw.SendDone()
				return
			}
			dl.Errorf("stream error: %v", err)
			_ = sw.Send("error", sse.ErrorData{Code: "upstream_error", Message: err.Error()})
			return
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta

		if delta.Content != nil && *delta.Content != "" {
			_ = sw.Send("delta", sse.DeltaData{Content: *delta.Content})
		}
	}
}
