package api

import (
	"testing"

	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
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
