// Package codex is the spike's subscription adapter for the chatgpt codex
// responses backend, informed by pi-ai 0.87.1. it speaks SSE with full
// history and store=false only: no websocket, no previous_response_id, no
// hidden provider session.
package codex

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/michaelquigley/pane/spike/openai-subscription/round"
)

const (
	// ServiceIdentity names the provider service in replay identity.
	ServiceIdentity = "chatgpt.com/backend-api/codex"

	// ResponsesURL is the only destination that receives a bearer token for
	// generation.
	ResponsesURL = "https://chatgpt.com/backend-api/codex/responses"
)

// Credentials supplies a current access token and its account binding. the
// implementation owns refresh; the adapter never sees refresh tokens.
type Credentials interface {
	// Access returns a current access token and the raw account id header
	// value. neither may be logged.
	Access(ctx context.Context) (token string, accountID string, err error)
	// AccountScope returns the non-secret account binding for replay
	// identity.
	AccountScope(ctx context.Context) (string, error)
}

// ErrDestination is returned when a credentialed request would leave the
// approved destination set.
var ErrDestination = errors.New("credential destination refused")

// lockedTransport attaches credentials only to an exact allow-listed URL and
// refuses everything else before any bytes leave the process.
type lockedTransport struct {
	base    http.RoundTripper
	allowed map[string]bool // scheme://host/path
	creds   Credentials
}

func destinationKey(r *http.Request) string {
	return r.URL.Scheme + "://" + r.URL.Host + r.URL.Path
}

func (t *lockedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !t.allowed[destinationKey(r)] || r.URL.User != nil || r.URL.RawQuery != "" {
		return nil, fmt.Errorf("%w: '%s'", ErrDestination, r.URL.Redacted())
	}
	token, account, err := t.creds.Access(r.Context())
	if err != nil {
		return nil, &round.Error{Kind: round.ErrAuth, Message: "no usable subscription credentials; run the login command", Err: err}
	}
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("chatgpt-account-id", account)
	return t.base.RoundTrip(r)
}

// newLockedClient builds the only http client that ever carries subscription
// credentials. redirects are refused outright.
func newLockedClient(base http.RoundTripper, creds Credentials, allowedURLs ...string) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	allowed := make(map[string]bool, len(allowedURLs))
	for _, u := range allowedURLs {
		allowed[u] = true
	}
	return &http.Client{
		Transport: &lockedTransport{base: base, allowed: allowed, creds: creds},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return fmt.Errorf("%w: redirect to '%s'", ErrDestination, req.URL.Redacted())
		},
	}
}
