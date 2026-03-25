package api

import (
	"encoding/json"
	"net/http"

	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
)

type API struct {
	cfg *config.Config
	llm *llm.Client
}

func NewAPI(cfg *config.Config, llmClient *llm.Client) *API {
	return &API{cfg: cfg, llm: llmClient}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/models", a.handleModels)
	mux.HandleFunc("POST /api/chat", a.handleChat)
}

func (a *API) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
