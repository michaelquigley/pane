package api

import (
	"net/http"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/mcp"
)

type API struct {
	cfg       *config.Config
	llm       *llm.Client
	mcp       *mcp.Manager
	approvals *ApprovalRegistry
}

type healthResponse struct {
	Status string
}

type configResponse struct {
	DefaultSystem        string
	DefaultModel         string
	MCPSeparator         string
	ContextWindows       map[string]int `dd:",+omitempty"`
	DefaultContextWindow int            `dd:",+omitempty"`
}

func NewAPI(cfg *config.Config, llmClient *llm.Client, mcpMgr *mcp.Manager) *API {
	return &API{
		cfg:       cfg,
		llm:       llmClient,
		mcp:       mcpMgr,
		approvals: NewApprovalRegistry(),
	}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/config", a.handleConfig)
	mux.HandleFunc("GET /api/models", a.handleModels)
	mux.HandleFunc("POST /api/chat", a.handleChat)
	mux.HandleFunc("GET /api/tools", a.handleTools)
	mux.HandleFunc("POST /api/tools/approve", a.handleApprove)
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = dd.UnbindJSONWriter(healthResponse{Status: "ok"}, w)
}

func (a *API) handleConfig(w http.ResponseWriter, _ *http.Request) {
	separator := "_"
	if a.cfg.MCP != nil && a.cfg.MCP.Separator != "" {
		separator = a.cfg.MCP.Separator
	}

	w.Header().Set("Content-Type", "application/json")
	_ = dd.UnbindJSONWriter(configResponse{
		DefaultSystem:        a.cfg.System,
		DefaultModel:         a.cfg.Model,
		MCPSeparator:         separator,
		ContextWindows:       a.cfg.ContextWindows,
		DefaultContextWindow: a.cfg.DefaultContextWindow,
	}, w)
}
