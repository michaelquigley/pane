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
	AuthorizeURL      = AuthBase + "/oauth/authorize"
	TokenURL          = AuthBase + "/oauth/token"
	RedirectURI       = "http://localhost:1455/auth/callback"
	DeviceUserCodeURL = AuthBase + "/api/accounts/deviceauth/usercode"
	DeviceTokenURL    = AuthBase + "/api/accounts/deviceauth/token"
	DeviceVerifyURL   = AuthBase + "/codex/device"
	DeviceRedirectURI = AuthBase + "/deviceauth/callback"
	Scope             = "openid profile email offline_access"
)

const refreshWindow = 5 * time.Minute

type endpoints struct{ token, deviceStart, devicePoll string }

var productionEndpoints = endpoints{TokenURL, DeviceUserCodeURL, DeviceTokenURL}

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

type RefreshError struct {
	Rejected bool
	Status   int
}

func (e *RefreshError) Error() string {
	if e.Rejected {
		return fmt.Sprintf("refresh rejected (status %d): login required", e.Status)
	}
	return fmt.Sprintf("refresh failed (status %d)", e.Status)
}
func (e *RefreshError) Unwrap() error {
	if e.Rejected {
		return ErrLoginRequired
	}
	return nil
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

func (m *Manager) access(ctx context.Context, expected string) (string, string, error) {
	c, err := m.store.Read()
	if err != nil {
		return "", "", err
	}
	if expected != "" && c.AccountID != expected {
		return "", "", errors.New("subscription account changed during request")
	}
	if m.now().Add(refreshWindow).Before(c.ExpiresAt) {
		return c.Access, c.AccountID, nil
	}
	var out *Credential
	err = m.store.withLock(ctx, func() error {
		current, err := m.store.Read()
		if err != nil {
			return err
		}
		if expected != "" && current.AccountID != expected {
			return errors.New("subscription account changed during request")
		}
		if m.now().Add(refreshWindow).Before(current.ExpiresAt) {
			out = current
			return nil
		}
		next, err := m.refresh(ctx, current.Refresh)
		if err != nil {
			return err
		}
		if next.AccountID != current.AccountID {
			return errors.New("refresh returned a different account; credentials unchanged")
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

type Status struct {
	SignedIn      bool
	RefreshNeeded bool
}

func (m *Manager) Status() (Status, error) {
	c, err := m.store.Read()
	if errors.Is(err, ErrLoginRequired) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	return Status{SignedIn: true, RefreshNeeded: !m.now().Add(refreshWindow).Before(c.ExpiresAt)}, nil
}

func (m *Manager) Logout(ctx context.Context) error { return m.store.Logout(ctx) }

type tokenResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

func (m *Manager) refresh(ctx context.Context, token string) (*Credential, error) {
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {ClientID}}
	return m.tokenRequest(ctx, form)
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
		return nil, &RefreshError{Rejected: true, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &RefreshError{Status: resp.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return nil, errors.New("invalid token response")
	}
	var tr tokenResponse
	if dd.BindJSON(&tr, raw) != nil || tr.AccessToken == "" || tr.RefreshToken == "" || tr.ExpiresIn <= 0 {
		return nil, errors.New("invalid token response")
	}
	account, err := AccountIDFromJWT(tr.AccessToken)
	if err != nil {
		return nil, err
	}
	return &Credential{Access: tr.AccessToken, Refresh: tr.RefreshToken, ExpiresAt: m.now().Add(time.Duration(tr.ExpiresIn) * time.Second), AccountID: account}, nil
}
