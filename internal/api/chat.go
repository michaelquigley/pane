package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/sse"
)

type chatRequest struct {
	Model            string
	Messages         []llm.Message
	SystemPromptMode string
	SystemPrompt     string
}

func (a *API) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := dd.BindJSONReader(&req, r.Body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	model, ok := a.cfg.ResolveModel(req.Model)
	if !ok {
		selected := req.Model
		if strings.TrimSpace(selected) == "" {
			selected = a.cfg.Model
		}
		http.Error(w, fmt.Sprintf("unknown model '%s'", selected), http.StatusBadRequest)
		return
	}
	llmClient := a.llm
	if a.cfg.HasModelRegistry() {
		llmClient = a.modelClients[model.Alias]
		if llmClient == nil {
			http.Error(w, fmt.Sprintf("model '%s' is unavailable", model.Alias), http.StatusInternalServerError)
			return
		}
	}
	req.Messages = buildChatMessages(req.Messages, resolveSystemPrompt(req, a.cfg))

	sw, err := sse.NewWriter(w)
	if err != nil {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	tools := a.mcp.GetAllModelTools()

	if err := llm.RunToolLoop(r.Context(), llmClient, req.Messages, model.UpstreamModel, model.MaxTokens, tools, a.mcp, chatEventSink{writer: sw}, a.approvals); err != nil {
		dl.Errorf("tool loop: %v", err)
	}
}

func resolveSystemPrompt(req chatRequest, cfg *config.Config) string {
	switch req.SystemPromptMode {
	case "custom":
		if strings.TrimSpace(req.SystemPrompt) == "" {
			return ""
		}
		return req.SystemPrompt
	case "none":
		return ""
	default:
		return cfg.System
	}
}

func buildChatMessages(messages []llm.Message, systemPrompt string) []llm.Message {
	filtered := make([]llm.Message, 0, len(messages)+1)
	if strings.TrimSpace(systemPrompt) != "" {
		filtered = append(filtered, llm.Message{
			Role:    "system",
			Content: llm.StringContent(systemPrompt),
		})
	}

	for _, message := range messages {
		if message.Role == "system" {
			continue
		}
		filtered = append(filtered, message)
	}

	return filtered
}
