package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/auth"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/llm/codex"
	"github.com/michaelquigley/pane/internal/session"
	"github.com/michaelquigley/pane/internal/sse"
)

// maxChatBody is the aggregate chat-body budget: the same cap the session
// store applies to one document.
const maxChatBody = session.MaxDocumentSize

type chatRequest struct {
	Model            string
	Messages         []llm.Message
	SystemPromptMode string
	SystemPrompt     string
	// TurnID and Recovery are the browser's turn contract: both present, or
	// both absent for a legacy client.
	TurnID   string
	Recovery *recoveryProjection
}

type chatErrorBody struct {
	Code    string
	Message string
}

type chatErrorResponse struct {
	Error chatErrorBody
}

// writeChatError reports a typed failure before any stream opens: nothing
// was generated and no tool ran.
func writeChatError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = dd.UnbindJSONWriter(chatErrorResponse{Error: chatErrorBody{Code: code, Message: message}}, w)
}

// handleChat runs one submitted turn. it validates the full supplied history
// and recovery projection, resolves the turn's connection, and only then
// opens the stream. it never reads the conversation store.
func (a *API) handleChat(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxChatBody)
	var req chatRequest
	if err := dd.BindJSONReader(&req, r.Body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeChatError(w, http.StatusRequestEntityTooLarge, "request_too_large", fmt.Sprintf("chat request exceeds %d bytes", maxChatBody))
			return
		}
		writeChatError(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}

	model, ok := a.cfg.ResolveModel(req.Model)
	if !ok {
		selected := req.Model
		if strings.TrimSpace(selected) == "" {
			selected = a.cfg.Model
		}
		writeChatError(w, http.StatusBadRequest, "unknown_model", fmt.Sprintf("unknown model '%s'", selected))
		return
	}

	turnID := req.TurnID
	switch {
	case req.TurnID == "" && req.Recovery == nil:
		// a legacy client: a server-generated turn id and the event stream,
		// without prior-recovery validation or the save-barrier guarantee.
		turnID = llm.NewTurnID()
	case req.TurnID == "" || req.Recovery == nil:
		writeChatError(w, http.StatusBadRequest, "invalid_recovery", "turn_id and recovery must be supplied together")
		return
	default:
		if err := validateRecovery(req.Messages, req.TurnID, req.Recovery); err != nil {
			status := http.StatusBadRequest
			if err.Code == "recovery_required" {
				status = http.StatusConflict
			}
			writeChatError(w, status, err.Code, err.Message)
			return
		}
	}

	adapter, origin, err := a.connect(r.Context(), model)
	if err != nil {
		var chatErr *chatErrorBody
		if errors.As(err, &chatErr) {
			writeChatError(w, http.StatusServiceUnavailable, chatErr.Code, chatErr.Message)
			return
		}
		writeChatError(w, http.StatusInternalServerError, "unavailable", err.Error())
		return
	}
	messages := buildChatMessages(req.Messages, resolveSystemPrompt(req, a.cfg))

	sw, err := sse.NewWriter(w)
	if err != nil {
		writeChatError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming not supported")
		return
	}

	toolSource := a.tools
	if toolSource == nil {
		toolSource = a.mcp
	}
	tools := toolSource.GetAllModelTools()
	sink := &chatEventSink{writer: sw, turnID: turnID}
	turn := llm.Turn{ID: turnID, Alias: model.Alias, Origin: origin}
	err = llm.RunToolLoop(r.Context(), adapter, turn, messages, model.UpstreamModel, model.MaxTokens, tools, toolSource, sink, a.approvals)
	if err != nil {
		dl.Errorf("tool loop: %v", err)
	}
	a.observeTurn(model, sink)
}

func (e *chatErrorBody) Error() string { return e.Message }

// connect resolves the submitted turn's immutable connection. a subscription
// alias captures its account binding here; later token rotation stays bound
// to that account for every round of the turn.
func (a *API) connect(ctx context.Context, model config.ResolvedModel) (llm.RoundAdapter, *llm.RoundOrigin, error) {
	if model.Provider == config.ProviderCodex {
		if a.subscription == nil {
			message := "subscription credentials are unavailable"
			if a.subscriptionErr != nil {
				message = fmt.Sprintf("subscription credentials are unavailable: %v", a.subscriptionErr)
			}
			return nil, nil, &chatErrorBody{Code: "auth_error", Message: message}
		}
		effort := ""
		if model.ReasoningEffort != nil {
			effort = *model.ReasoningEffort
		}
		adapter, err := codex.New(ctx, model.Alias, model.UpstreamModel, effort, a.subscription, a.codexTransport)
		if errors.Is(err, auth.ErrLoginRequired) {
			return nil, nil, &chatErrorBody{Code: "login_required", Message: fmt.Sprintf("model '%s' needs a subscription login: run 'pane auth login openai'", model.Alias)}
		}
		if err != nil {
			return nil, nil, &chatErrorBody{Code: "auth_error", Message: fmt.Sprintf("model '%s' credentials are unusable: %v", model.Alias, err)}
		}
		origin := adapter.Origin()
		return adapter, &origin, nil
	}

	client := a.llm
	if a.cfg.HasModelRegistry() {
		client = a.modelClients[model.Alias]
		if client == nil {
			return nil, nil, fmt.Errorf("model '%s' is unavailable", model.Alias)
		}
	}
	origin := client.Origin(model.Alias, model.UpstreamModel)
	return client, &origin, nil
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
