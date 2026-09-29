package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/michaelquigley/df/dd"
)

const (
	ClientID          = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthBase          = "https://auth.openai.com"
	TokenURL          = AuthBase + "/oauth/token"
	DeviceUserCodeURL = AuthBase + "/api/accounts/deviceauth/usercode"
	DeviceTokenURL    = AuthBase + "/api/accounts/deviceauth/token"
	DeviceVerifyURL   = AuthBase + "/codex/device"
	DeviceRedirectURI = AuthBase + "/deviceauth/callback"
)

type endpoints struct{ token, deviceStart, devicePoll string }

var productionEndpoints = endpoints{TokenURL, DeviceUserCodeURL, DeviceTokenURL}

// Manager supplies the one device-login credential to every subscription
// alias. it never renews a credential: an expired or removed login calls for
// another explicit 'pane auth login openai'.
type Manager struct {
	store     *Store
	client    *LockedClient
	endpoints endpoints
	now       func() time.Time
}

func NewManager(store *Store) *Manager { return newManager(store, nil, productionEndpoints) }

func newManager(store *Store, base http.RoundTripper, dest endpoints) *Manager {
	return &Manager{store: store, client: lockedClient(base, dest.token, dest.deviceStart, dest.devicePoll), endpoints: dest, now: time.Now}
}

// TokenError reports a failed authorization-code exchange.
type TokenError struct {
	Rejected bool
	Status   int
}

func (e *TokenError) Error() string {
	if e.Rejected {
		return fmt.Sprintf("token exchange rejected (status %d)", e.Status)
	}
	return fmt.Sprintf("token exchange failed (status %d)", e.Status)
}

// current reads the stored credential and treats it as absent once the
// clock reaches its expiry: 'now >= expires_at' means login required.
func (m *Manager) current() (*Credential, error) {
	c, err := m.store.Read()
	if err != nil {
		return nil, err
	}
	if !m.now().Before(c.ExpiresAt) {
		return nil, ErrLoginRequired
	}
	return c, nil
}

// account-bound access prevents a mid-turn login from silently changing identity.
func (m *Manager) AccessForAccount(ctx context.Context, expected string) (string, error) {
	access, _, err := m.access(ctx, expected)
	if err != nil {
		return "", err
	}
	return access, nil
}

func (m *Manager) Access(ctx context.Context) (string, string, error) {
	return m.access(ctx, "")
}

// CurrentAccount reads the selected account of an unexpired login.
func (m *Manager) CurrentAccount(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c, err := m.current()
	if err != nil {
		return "", err
	}
	return c.AccountID, nil
}

// access is a local read: it makes no network request and never renews.
func (m *Manager) access(ctx context.Context, expected string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	c, err := m.current()
	if err != nil {
		return "", "", err
	}
	if expected != "" && c.AccountID != expected {
		return "", "", errors.New("subscription account changed during request")
	}
	return c.Access, c.AccountID, nil
}

type Status struct {
	// SignedIn means an unexpired credential is stored and usable locally;
	// Expired means its expiry has been reached and another login is needed.
	SignedIn bool
	Expired  bool
	// ExpiryMarker is the stored credential's expiration time, empty when
	// signed out. login and logout normally change it, but a replacement
	// with the same expiry keeps it; it is not a credential version. a
	// non-secret comparison value, never shown.
	ExpiryMarker string
}

// Status inspects the local credential only: no network.
func (m *Manager) Status() (Status, error) {
	c, err := m.store.Read()
	if errors.Is(err, ErrLoginRequired) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	marker := fmt.Sprintf("%d", c.ExpiresAt.UnixNano())
	if !m.now().Before(c.ExpiresAt) {
		return Status{Expired: true, ExpiryMarker: marker}, nil
	}
	return Status{SignedIn: true, ExpiryMarker: marker}, nil
}

func (m *Manager) Logout(ctx context.Context) error { return m.store.Logout(ctx) }

// tokenResponse binds only what pane keeps. a refresh token in the response
// is ignored: pane never renews a credential.
type tokenResponse struct {
	AccessToken string
	ExpiresIn   int
}

func (m *Manager) tokenRequest(ctx context.Context, form url.Values) (*Credential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoints.token, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid token destination")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New("token request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &TokenError{Rejected: true, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &TokenError{Status: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return nil, errors.New("invalid token response")
	}
	var tr tokenResponse
	if dd.BindJSON(&tr, raw) != nil || tr.AccessToken == "" || tr.ExpiresIn <= 0 {
		return nil, errors.New("invalid token response")
	}
	account, err := AccountIDFromJWT(tr.AccessToken)
	if err != nil {
		return nil, err
	}
	return &Credential{Access: tr.AccessToken, ExpiresAt: m.now().Add(time.Duration(tr.ExpiresIn) * time.Second), AccountID: account}, nil
}
