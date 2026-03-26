package api

import (
	"encoding/json"
	"net/http"

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
	mux.HandleFunc("POST /api/tools/toggle", a.handleToolToggle)
	mux.HandleFunc("POST /api/tools/approve", a.handleApprove)
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (a *API) handleConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"default_system": a.cfg.System,
		"default_model":  a.cfg.Model,
	})
}
