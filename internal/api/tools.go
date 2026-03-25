package api

import (
	"encoding/json"
	"net/http"

	"github.com/michaelquigley/pane/internal/mcp"
)

type toolsResponse struct {
	Tools   []mcp.ToolInfo              `json:"tools"`
	Servers map[string]mcp.ServerStatus `json:"servers"`
}

func (a *API) handleTools(w http.ResponseWriter, _ *http.Request) {
	resp := toolsResponse{
		Tools:   a.mcp.GetAllTools(),
		Servers: a.mcp.GetServerStatuses(),
	}
	if resp.Tools == nil {
		resp.Tools = []mcp.ToolInfo{}
	}
	if resp.Servers == nil {
		resp.Servers = map[string]mcp.ServerStatus{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type toggleRequest struct {
	Tool    string `json:"tool"`
	Enabled bool   `json:"enabled"`
}

func (a *API) handleToolToggle(w http.ResponseWriter, r *http.Request) {
	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := a.mcp.ToggleTool(req.Tool, req.Enabled); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
