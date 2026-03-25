package api

import (
	"encoding/json"
	"net/http"

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

	tools := a.mcp.GetEnabledTools(req.ToolsDisabled)

	if err := llm.RunToolLoop(r.Context(), a.llm, req.Messages, model, tools, a.mcp, sw, a.approvals); err != nil {
		dl.Errorf("tool loop: %v", err)
	}
}
