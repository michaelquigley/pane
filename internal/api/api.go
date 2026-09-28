package api

import (
	"net/http"
	"sync"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/pane/internal/auth"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/llm/codex"
	"github.com/michaelquigley/pane/internal/mcp"
	"github.com/michaelquigley/pane/internal/session"
)

type API struct {
	cfg          *config.Config
	llm          *llm.Client
	modelClients map[string]*llm.Client
	mcp          *mcp.Manager
	// tools is the chat path's tool source and executor: the MCP manager in
	// production, replaceable in tests that count executor calls.
	tools     chatTools
	sessions  *session.Store
	approvals *ApprovalRegistry

	// subscription is the shared credential manager for every 'openai-codex'
	// alias; subscriptionErr records why it could not be opened, which is an
	// availability state rather than a startup failure.
	subscription    SubscriptionAuth
	subscriptionErr error
	codexTransport  http.RoundTripper

	failuresMu sync.Mutex
	failures   map[string]modelFailure
}

type chatTools interface {
	llm.ToolExecutor
	GetAllModelTools() []llm.Tool
}

// SubscriptionAuth is the credential surface the API needs: account-bound
// access for generation and local status for availability.
type SubscriptionAuth interface {
	codex.Credentials
	Status() (auth.Status, error)
}

// SetSubscriptionAuth installs the shared subscription credential manager,
// or the error that prevented opening it.
func (a *API) SetSubscriptionAuth(subscription SubscriptionAuth, err error) {
	a.subscription, a.subscriptionErr = subscription, err
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

func NewAPI(cfg *config.Config, llmClient *llm.Client, modelClients map[string]*llm.Client, mcpMgr *mcp.Manager, sessions *session.Store) *API {
	return &API{
		cfg:          cfg,
		llm:          llmClient,
		modelClients: modelClients,
		mcp:          mcpMgr,
		tools:        mcpMgr,
		sessions:     sessions,
		approvals:    NewApprovalRegistry(),
	}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/config", a.handleConfig)
	mux.HandleFunc("GET /api/models", a.handleModels)
	mux.HandleFunc("POST /api/chat", a.handleChat)
	mux.HandleFunc("GET /api/tools", a.handleTools)
	mux.HandleFunc("POST /api/tools/approve", a.handleApprove)
	mux.HandleFunc("GET /api/sessions", a.handleListSessions)
	mux.HandleFunc("GET /api/sessions/{id}", a.handleGetSession)
	mux.HandleFunc("PUT /api/sessions/{id}", a.handleSaveSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", a.handleDeleteSession)
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
	contextWindows := a.cfg.ContextWindows
	defaultContextWindow := a.cfg.DefaultContextWindow
	if a.cfg.HasModelRegistry() {
		contextWindows = nil
		defaultContextWindow = 0
		for _, model := range a.cfg.ResolvedModels() {
			if model.ContextWindow <= 0 {
				continue
			}
			if contextWindows == nil {
				contextWindows = make(map[string]int)
			}
			contextWindows[model.Alias] = model.ContextWindow
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = dd.UnbindJSONWriter(configResponse{
		DefaultSystem:        a.cfg.System,
		DefaultModel:         a.cfg.Model,
		MCPSeparator:         separator,
		ContextWindows:       contextWindows,
		DefaultContextWindow: defaultContextWindow,
	}, w)
}
