package api

import (
	"encoding/json"
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
