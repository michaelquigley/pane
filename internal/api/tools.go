package api

import (
	"encoding/json"
	"net/http"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/mcp"
)

type toolsResponse struct {
	Tools   []toolInfoResponse
	Servers map[string]mcp.ServerStatus
}

type toolInfoResponse struct {
	Server   string
	Name     string
	Function toolFunctionResponse
}

type toolFunctionResponse struct {
	Name        string
	Description string
	Parameters  json.RawMessage `dd:"-"`
}

func (f toolFunctionResponse) MarshalDd() (map[string]any, error) {
	return map[string]any{
		"name": f.Name, "description": f.Description,
		"parameters": f.Parameters,
	}, nil
}

func (a *API) handleTools(w http.ResponseWriter, _ *http.Request) {
	resp := toolsResponse{
		Tools:   []toolInfoResponse{},
		Servers: a.mcp.GetServerStatuses(),
	}
	for _, tool := range a.mcp.GetAllTools() {
		_, err := dd.DecodeStrictJSON(tool.Function.Parameters)
		if err != nil {
			http.Error(w, "tool schema unavailable", http.StatusInternalServerError)
			return
		}
		resp.Tools = append(resp.Tools, toolInfoResponse{
			Server: tool.Server,
			Name:   tool.Name,
			Function: toolFunctionResponse{
				Name: tool.Function.Name, Description: tool.Function.Description,
				Parameters: tool.Function.Parameters,
			},
		})
	}
	if resp.Servers == nil {
		resp.Servers = map[string]mcp.ServerStatus{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = dd.UnbindJSONWriter(resp, w)
}
