package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/sse"
)

type chatRequest struct {
	Model            string        `json:"model"`
	Messages         []llm.Message `json:"messages"`
	ToolsDisabled    []string      `json:"tools_disabled"`
	SystemPromptMode string        `json:"system_prompt_mode"`
	SystemPrompt     string        `json:"system_prompt"`
}

func (a *API) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	model := resolveModel(req.Model, a.cfg)
	req.Messages = buildChatMessages(req.Messages, resolveSystemPrompt(req, a.cfg))

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

func resolveModel(override string, cfg *config.Config) string {
	if strings.TrimSpace(override) == "" {
		return cfg.Model
	}
	return override
}

func resolveSystemPrompt(req chatRequest, cfg *config.Config) string {
	switch normalizeSystemPromptMode(req.SystemPromptMode, req.SystemPrompt) {
	case "custom":
		return req.SystemPrompt
	case "none":
		return ""
	default:
		return cfg.System
	}
}

func normalizeSystemPromptMode(mode, custom string) string {
	switch mode {
	case "custom":
		if strings.TrimSpace(custom) == "" {
			return "none"
		}
		return "custom"
	case "none":
		return "none"
	default:
		return "default"
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
