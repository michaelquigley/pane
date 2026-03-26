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
