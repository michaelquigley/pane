package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/mcp"
)

func TestResolveModelUsesDefaultWhenOverrideIsBlank(t *testing.T) {
	cfg := &config.Config{Model: "configured-model"}

	got := resolveModel("", cfg)

	if got != "configured-model" {
		t.Fatalf("expected configured model, got %q", got)
	}
}

func TestResolveModelUsesOverrideWhenProvided(t *testing.T) {
	cfg := &config.Config{Model: "configured-model"}

	got := resolveModel("override-model", cfg)

	if got != "override-model" {
		t.Fatalf("expected override model, got %q", got)
	}
}

func TestResolveSystemPromptDefaultModeUsesConfigPrompt(t *testing.T) {
	cfg := &config.Config{System: "configured prompt"}

	got := resolveSystemPrompt(chatRequest{SystemPromptMode: "default"}, cfg)

	if got != "configured prompt" {
		t.Fatalf("expected configured prompt, got %q", got)
	}
}

func TestResolveSystemPromptCustomModeUsesCustomPrompt(t *testing.T) {
	cfg := &config.Config{System: "configured prompt"}

	got := resolveSystemPrompt(chatRequest{
		SystemPromptMode: "custom",
		SystemPrompt:     "custom prompt",
	}, cfg)

	if got != "custom prompt" {
		t.Fatalf("expected custom prompt, got %q", got)
	}
}

func TestResolveSystemPromptNoneModeSkipsPrompt(t *testing.T) {
	cfg := &config.Config{System: "configured prompt"}

	got := resolveSystemPrompt(chatRequest{SystemPromptMode: "none"}, cfg)

	if got != "" {
		t.Fatalf("expected no prompt, got %q", got)
	}
}

func TestResolveSystemPromptBlankCustomNormalizesToNone(t *testing.T) {
	cfg := &config.Config{System: "configured prompt"}

	got := resolveSystemPrompt(chatRequest{
		SystemPromptMode: "custom",
		SystemPrompt:     "   ",
	}, cfg)

	if got != "" {
		t.Fatalf("expected blank custom prompt to skip prompt, got %q", got)
	}
}

func TestBuildChatMessagesPrependsResolvedSystemPromptAndFiltersExistingSystemMessages(t *testing.T) {
	messages := []llm.Message{
		{Role: "system", Content: llm.StringContent("stale prompt")},
		{Role: "user", Content: llm.StringContent("hello")},
		{Role: "assistant", Content: llm.StringContent("hi")},
	}

	got := buildChatMessages(messages, "resolved prompt")

	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	if got[0].Role != "system" || got[0].Content == nil || *got[0].Content != "resolved prompt" {
		t.Fatalf("expected resolved system prompt first, got %#v", got[0])
	}

	if got[1].Role != "user" {
		t.Fatalf("expected user message second, got %q", got[1].Role)
	}

	if got[2].Role != "assistant" {
		t.Fatalf("expected assistant message third, got %q", got[2].Role)
	}
}

func TestBuildChatMessagesSkipsSystemPromptWhenNoneResolved(t *testing.T) {
	messages := []llm.Message{
		{Role: "user", Content: llm.StringContent("hello")},
	}

	got := buildChatMessages(messages, "")

	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}

	if got[0].Role != "user" {
		t.Fatalf("expected user message, got %q", got[0].Role)
	}
}

func TestHandleChatStripsReasoningFieldsFromUpstreamRequest(t *testing.T) {
	var bodiesMu sync.Mutex
	var upstreamBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading request body: %v", err)
		}
		bodiesMu.Lock()
		upstreamBody = string(body)
		bodiesMu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"chat-1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	api := &API{
		cfg:       &config.Config{Model: "test-model"},
		llm:       llm.NewClient(server.URL, "test-model", ""),
		mcp:       mcp.NewManager(nil),
		approvals: NewApprovalRegistry(),
	}

	// the request body carries stray reasoning fields under both known
	// frontend spellings; neither may reach the upstream client
	reqBody := `{"model":"","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi","reasoning":"stray reasoning","thinking":"stray thinking"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	api.handleChat(httptest.NewRecorder(), req)

	bodiesMu.Lock()
	body := upstreamBody
	bodiesMu.Unlock()

	if body == "" {
		t.Fatalf("upstream client received no request")
	}
	if strings.Contains(body, "reasoning") {
		t.Fatalf("upstream request body carries reasoning: %s", body)
	}
	if strings.Contains(body, "thinking") {
		t.Fatalf("upstream request body carries thinking: %s", body)
	}
	if !strings.Contains(body, "hello") || !strings.Contains(body, "hi") {
		t.Fatalf("upstream request body lost the message history: %s", body)
	}
}
