package api

import (
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

	model := resolveModel(req.Model, a.cfg)
	req.Messages = buildChatMessages(req.Messages, resolveSystemPrompt(req, a.cfg))

	sw, err := sse.NewWriter(w)
	if err != nil {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	tools := a.mcp.GetEnabledTools()

	if err := llm.RunToolLoop(r.Context(), a.llm, req.Messages, model, resolveMaxTokens(model, a.cfg), tools, a.mcp, sw, a.approvals); err != nil {
		dl.Errorf("tool loop: %v", err)
	}
}

func resolveModel(override string, cfg *config.Config) string {
	if strings.TrimSpace(override) == "" {
		return cfg.Model
	}
	return override
}

// resolveMaxTokens picks the output token cap for the resolved model: the
// per-model value when configured, else the default. zero means the
// backend's own output budget applies, so the field is left off the request.
func resolveMaxTokens(model string, cfg *config.Config) int {
	if cap, ok := cfg.MaxTokens[model]; ok {
		return cap
	}
	return cfg.DefaultMaxTokens
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
