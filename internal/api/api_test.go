package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/michaelquigley/pane/internal/config"
)

func TestHandleConfigReturnsDefaultFields(t *testing.T) {
	api := &API{
		cfg: &config.Config{
			Model:  "configured-model",
			System: "configured system",
			MCP: &config.MCPConfig{
				Separator: "::",
			},
		},
	}

	recorder := httptest.NewRecorder()

	api.handleConfig(recorder, httptest.NewRequest("GET", "/api/config", nil))

	var payload map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if payload["default_model"] != "configured-model" {
		t.Fatalf("expected default_model, got %q", payload["default_model"])
	}

	if payload["default_system"] != "configured system" {
		t.Fatalf("expected default_system, got %q", payload["default_system"])
	}

	if payload["mcp_separator"] != "::" {
		t.Fatalf("expected mcp_separator, got %q", payload["mcp_separator"])
	}
}

func TestHandleConfigReturnsContextWindowsWhenConfigured(t *testing.T) {
	api := &API{
		cfg: &config.Config{
			Model:                "configured-model",
			System:               "configured system",
			ContextWindows:       map[string]int{"configured-model": 32768, "gateway-model": 200000},
			DefaultContextWindow: 128000,
			IncludeUsage:         true,
			MCP: &config.MCPConfig{
				Separator: "::",
			},
		},
	}

	recorder := httptest.NewRecorder()

	api.handleConfig(recorder, httptest.NewRequest("GET", "/api/config", nil))

	var payload map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	windows, ok := payload["context_windows"].(map[string]any)
	if !ok {
		t.Fatalf("expected context_windows object, got %#v", payload["context_windows"])
	}
	if windows["configured-model"] != float64(32768) || windows["gateway-model"] != float64(200000) {
		t.Fatalf("unexpected context_windows payload: %#v", windows)
	}
	if payload["default_context_window"] != float64(128000) {
		t.Fatalf("unexpected default_context_window: %#v", payload["default_context_window"])
	}
	if _, ok := payload["include_usage"]; ok {
		t.Fatalf("include_usage must not be exposed through /api/config")
	}
}

func TestHandleConfigOmitsUnconfiguredWindowsAndIncludeUsage(t *testing.T) {
	for _, includeUsage := range []bool{true, false} {
		t.Run(fmt.Sprintf("include_usage_%v", includeUsage), func(t *testing.T) {
			api := &API{
				cfg: &config.Config{
					Model:        "configured-model",
					System:       "configured system",
					IncludeUsage: includeUsage,
				},
			}

			recorder := httptest.NewRecorder()

			api.handleConfig(recorder, httptest.NewRequest("GET", "/api/config", nil))

			var payload map[string]any
			if err := json.NewDecoder(recorder.Body).Decode(&payload); err != nil {
				t.Fatalf("decoding response: %v", err)
			}
			if _, ok := payload["context_windows"]; ok {
				t.Fatalf("expected context_windows to be omitted: %#v", payload)
			}
			if _, ok := payload["default_context_window"]; ok {
				t.Fatalf("expected default_context_window to be omitted: %#v", payload)
			}
			if _, ok := payload["include_usage"]; ok {
				t.Fatalf("include_usage must not be exposed through /api/config")
			}
		})
	}
}
