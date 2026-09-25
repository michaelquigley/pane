package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/mcp"
)

func TestHandleToolsReturnsEmptyCollections(t *testing.T) {
	a := &API{mcp: mcp.NewManager(&config.MCPConfig{})}
	w := httptest.NewRecorder()
	a.handleTools(w, httptest.NewRequest(http.MethodGet, "/api/tools", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if tools, ok := body["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	if servers, ok := body["servers"].(map[string]any); !ok || len(servers) != 0 {
		t.Fatalf("servers = %#v", body["servers"])
	}
}

func TestToolsResponsePreservesNumericSchemaConstraints(t *testing.T) {
	data, err := dd.UnbindJSON(toolsResponse{Tools: []toolInfoResponse{{
		Server: "local", Name: "read", Function: toolFunctionResponse{
			Name: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"minLength":2}}}`),
		},
	}}, Servers: map[string]mcp.ServerStatus{"local": {Status: "ready", ToolsCount: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	tool := body["tools"].([]any)[0].(map[string]any)
	function := tool["function"].(map[string]any)
	path := function["parameters"].(map[string]any)["properties"].(map[string]any)["path"].(map[string]any)
	if path["minLength"] != float64(2) {
		t.Fatalf("numeric schema constraint changed: %#v", path)
	}
	status := body["servers"].(map[string]any)["local"].(map[string]any)
	if status["tools_count"] != float64(1) {
		t.Fatalf("server status changed: %#v", status)
	}
	if _, ok := status["error"]; ok {
		t.Fatalf("empty server error was emitted: %#v", status)
	}
}

func TestHandleApproveBindsRequest(t *testing.T) {
	for _, approved := range []bool{true, false} {
		a := &API{approvals: NewApprovalRegistry()}
		pending := a.approvals.Register("call-1")
		body := `{"id":"call-1","approved":false}`
		if approved {
			body = `{"id":"call-1","approved":true}`
		}
		w := httptest.NewRecorder()
		a.handleApprove(w, httptest.NewRequest(http.MethodPost, "/api/tools/approve", strings.NewReader(body)))
		if w.Code != http.StatusNoContent {
			t.Fatalf("approved %v: status = %d, body = %q", approved, w.Code, w.Body.String())
		}
		if got := <-pending; got != approved {
			t.Fatalf("approved = %v, want %v", got, approved)
		}
	}
}

func TestHandleApproveRejectsMalformedBody(t *testing.T) {
	a := &API{approvals: NewApprovalRegistry()}
	a.approvals.Register("call-1")
	w := httptest.NewRecorder()
	a.handleApprove(w, httptest.NewRequest(http.MethodPost, "/api/tools/approve", strings.NewReader(`{"id":`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if len(a.approvals.pending) != 1 {
		t.Fatal("malformed request consumed pending approval")
	}
}
