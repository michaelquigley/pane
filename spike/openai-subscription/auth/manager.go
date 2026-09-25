package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// oauth constants, source-established from pi-ai 0.87.1
// (auth/oauth/openai-codex.js). the client id is the codex cli's public
// client; the codex 0.155.1 binary carries the same id.
const (
	ClientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthBase          = "https://auth.openai.com"
	AuthorizeURL      = AuthBase + "/oauth/authorize"
	TokenURL          = AuthBase + "/oauth/token"
	RedirectURI       = "http://localhost:1455/auth/callback"
	DeviceUserCodeURL = AuthBase + "/api/accounts/deviceauth/usercode"
	DeviceTokenURL    = AuthBase + "/api/accounts/deviceauth/token"
	DeviceVerifyURL   = AuthBase + "/codex/device"
	DeviceRedirectURI = AuthBase + "/deviceauth/callback"
	Scope             = "openid profile email offline_access"
)

// refreshWindow matches pi: refresh when under five minutes remain.
const refreshWindow = 5 * time.Minute

// Manager supplies current credentials to every subscription connection.
type Manager struct {
	store    *Store
	client   *http.Client
	tokenURL string
	now      func() time.Time
}

// NewManager builds the manager with the fixed token destination.
func NewManager(store *Store, base http.RoundTripper) *Manager {
	return newManager(store, base, TokenURL)
}

func newManager(store *Store, base http.RoundTripper, tokenURL string) *Manager {
	return &Manager{store: store, client: lockedAuthClient(base, tokenURL, DeviceUserCodeURL, DeviceTokenURL), tokenURL: tokenURL, now: time.Now}
}

// lockedAuthClient only talks to the oauth endpoints and refuses redirects.
func lockedAuthClient(base http.RoundTripper, allowed ...string) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !set[r.URL.Scheme+"://"+r.URL.Host+r.URL.Path] {
				return nil, fmt.Errorf("auth destination refused: '%s'", r.URL.Redacted())
			}
			return base.RoundTrip(r)
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
		Timeout:       15 * time.Second,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// RefreshError distinguishes rejected refresh (login again) from transient
// failure (keep credentials, try later).
type RefreshError struct {
	Rejected bool
	Status   int
	Err      error
}

func (e *RefreshError) Error() string {
	if e.Rejected {
		return fmt.Sprintf("refresh rejected (status %d): login required", e.Status)
	}
	return fmt.Sprintf("refresh failed transiently: %v", e.Err)
}

func (e *RefreshError) Unwrap() error {
	if e.Rejected {
		return ErrLoginRequired
	}
	return e.Err
}

// Access returns a current access token and account id, refreshing under
// the cross-process lock when needed.
func (m *Manager) Access(ctx context.Context) (string, string, error) {
	c, err := m.store.Read()
	if err != nil {
		return "", "", err
	}
	if m.now().Add(refreshWindow).Before(c.ExpiresAt) {
		return c.Access, c.AccountID, nil
	}
	var out *Credential
	err = m.store.withLock(ctx, func() error {
		cur, err := m.store.Read()
		if err != nil {
			return err // logged out meanwhile
		}
		if m.now().Add(refreshWindow).Before(cur.ExpiresAt) {
			out = cur // another process refreshed
			return nil
		}
		next, err := m.refresh(ctx, cur.Refresh)
		if err != nil {
			return err
		}
		if next.AccountID != cur.AccountID {
			return fmt.Errorf("refresh returned a different account; refusing to replace credentials")
		}
		if err := m.store.write(next); err != nil {
			return err
		}
		out = next
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return out.Access, out.AccountID, nil
}

// AccountScope returns the non-secret binding without refreshing.
func (m *Manager) AccountScope(ctx context.Context) (string, error) {
	c, err := m.store.Read()
	if err != nil {
		return "", err
	}
	return AccountScope(c.AccountID), nil
}

// Status reports safe metadata only.
type Status struct {
	SignedIn     bool      `json:"signed_in"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	AccountScope string    `json:"account_scope,omitempty"`
}

func (m *Manager) Status() Status {
	c, err := m.store.Read()
	if err != nil {
		return Status{}
	}
	return Status{SignedIn: true, ExpiresAt: c.ExpiresAt, AccountScope: AccountScope(c.AccountID)}
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (m *Manager) refresh(ctx context.Context, refreshToken string) (*Credential, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {ClientID}}
	return m.tokenRequest(ctx, form)
}

// tokenRequest posts to the token endpoint. response bodies are never
// echoed into errors because they may carry tokens.
func (m *Manager) tokenRequest(ctx context.Context, form url.Values) (*Credential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, &RefreshError{Err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &RefreshError{Rejected: true, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &RefreshError{Status: resp.StatusCode, Err: fmt.Errorf("token endpoint status %d", resp.StatusCode)}
	}
	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil || tr.AccessToken == "" || tr.RefreshToken == "" || tr.ExpiresIn <= 0 {
		return nil, &RefreshError{Err: errors.New("token response missing required fields")}
	}
	account, err := AccountIDFromJWT(tr.AccessToken)
	if err != nil {
		return nil, &RefreshError{Err: err}
	}
	return &Credential{
		Access:    tr.AccessToken,
		Refresh:   tr.RefreshToken,
		ExpiresAt: m.now().Add(time.Duration(tr.ExpiresIn) * time.Second),
		AccountID: account,
	}, nil
}
