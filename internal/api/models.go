package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/config"
)

// auth states reported for registry models. 'credential_available' means
// configured to attempt a request, not verified entitlement or health.
const (
	authNotRequired         = "not_required"
	authLoginRequired       = "login_required"
	authCredentialAvailable = "credential_available"
	authRefreshPending      = "refresh_pending"
	authError               = "error"
)

type modelsResponse struct {
	Object string
	Data   []modelEntry
}

// modelEntry extends the listing's model shape additively with safe
// availability metadata for registry aliases.
type modelEntry struct {
	ID        string
	Object    string
	OwnedBy   string
	Provider  string      `dd:",+omitempty"`
	AuthState string      `dd:",+omitempty"`
	LastError *modelError `dd:",+omitempty"`
}

type modelError struct {
	Code    string
	Message string
	At      int64
}

// modelFailure is the last observed runtime failure for one alias. auth
// failures carry the credential expiry marker they were observed under, so a
// login, refresh, or logout that changes the expiry retires them. a
// replacement with the same expiry keeps a stale auth warning until the next
// completed turn; that is accepted rather than versioning credentials.
type modelFailure struct {
	err    modelError
	auth   bool
	expiry string
}

func (a *API) handleModels(w http.ResponseWriter, r *http.Request) {
	if a.cfg.HasModelRegistry() {
		models := modelsResponse{Object: "list"}
		for _, model := range a.cfg.ResolvedModels() {
			entry := modelEntry{ID: model.Alias, Object: "model", OwnedBy: "pane", Provider: model.Provider}
			expiry := ""
			entry.AuthState, entry.LastError, expiry = a.authState(model)
			if failure, ok := a.lastFailure(model.Alias, expiry); ok && entry.LastError == nil {
				entry.LastError = &failure
			}
			models.Data = append(models.Data, entry)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = dd.UnbindJSONWriter(models, w)
		return
	}

	models, err := a.llm.ListModels(r.Context())
	if err != nil {
		dl.Errorf("listing models: %v", err)
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "unreachable") {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = dd.UnbindJSONWriter(models, w)
}

// authState inspects local credentials only: listing never probes a
// provider or refreshes a token.
func (a *API) authState(model config.ResolvedModel) (string, *modelError, string) {
	if model.Provider != config.ProviderCodex {
		return authNotRequired, nil, ""
	}
	if a.subscription == nil {
		message := "subscription credentials are unavailable"
		if a.subscriptionErr != nil {
			message = fmt.Sprintf("subscription credential store unavailable: %v", a.subscriptionErr)
		}
		return authError, &modelError{Code: "auth_error", Message: message, At: time.Now().UnixMilli()}, ""
	}
	status, err := a.subscription.Status()
	if err != nil {
		return authError, &modelError{Code: "auth_error", Message: "subscription credentials are unreadable; run 'pane auth login openai'", At: time.Now().UnixMilli()}, ""
	}
	switch {
	case !status.SignedIn:
		return authLoginRequired, nil, status.ExpiryMarker
	case status.RefreshNeeded:
		return authRefreshPending, nil, status.ExpiryMarker
	default:
		return authCredentialAvailable, nil, status.ExpiryMarker
	}
}

func (a *API) lastFailure(alias, expiry string) (modelError, bool) {
	a.failuresMu.Lock()
	defer a.failuresMu.Unlock()
	failure, ok := a.failures[alias]
	if !ok {
		return modelError{}, false
	}
	if failure.auth && failure.expiry != expiry {
		delete(a.failures, alias)
		return modelError{}, false
	}
	return failure.err, true
}

// observeTurn records a connection-level failure for the alias, or clears
// its last failure after a completed turn. failures never disable a model.
func (a *API) observeTurn(model config.ResolvedModel, sink *chatEventSink) {
	a.failuresMu.Lock()
	defer a.failuresMu.Unlock()
	if sink.end != nil && sink.end.Outcome == "completed" {
		delete(a.failures, model.Alias)
		return
	}
	if sink.failure == nil {
		return
	}
	message := ""
	auth := false
	switch sink.failure.Code {
	case "auth":
		message, auth = "the provider rejected the subscription credentials; run 'pane auth login openai' if this persists", true
	case "allowance":
		message = "the subscription allowance is exhausted or rate limited"
	case "upstream", "upstream_error", "transport":
		message = fmt.Sprintf("the last request failed ('%s')", sink.failure.Code)
	default:
		return
	}
	expiry := ""
	if auth && a.subscription != nil {
		if status, err := a.subscription.Status(); err == nil {
			expiry = status.ExpiryMarker
		}
	}
	if a.failures == nil {
		a.failures = make(map[string]modelFailure)
	}
	a.failures[model.Alias] = modelFailure{err: modelError{Code: sink.failure.Code, Message: message, At: time.Now().UnixMilli()}, auth: auth, expiry: expiry}
}
