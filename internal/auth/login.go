package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/michaelquigley/df/dd"
)

type Prompter func(string)

type deviceStartRequest struct{ ClientID string }
type devicePollRequest struct {
	DeviceAuthID string
	UserCode     string
}
type deviceStartResponse struct {
	DeviceAuthID string
	UserCode     string
	ExpiresIn    int
	Extra        map[string]any `dd:",+extra"`
}
type devicePollResponse struct {
	AuthorizationCode string
	CodeVerifier      string
}
type deviceErrorResponse struct{ Error string }

func pkce() (string, string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", err
	}
	verifier := base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (m *Manager) LoginBrowser(ctx context.Context, show Prompter) error {
	return m.loginBrowserAt(ctx, show, "127.0.0.1:1455", RedirectURI)
}

func (m *Manager) loginBrowserAt(ctx context.Context, show Prompter, address, redirect string) error {
	verifier, challenge, err := pkce()
	if err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	state := hex.EncodeToString(random[:])
	u, _ := url.Parse(AuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", redirect)
	q.Set("scope", Scope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", "pane")
	u.RawQuery = q.Encode()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("callback listener unavailable: %w", err)
	}
	codeCh := make(chan string, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if len(r.URL.Query()["state"]) != 1 || r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if len(r.URL.Query()["code"]) != 1 || r.URL.Query().Get("code") == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		select {
		case codeCh <- r.URL.Query().Get("code"):
			_, _ = io.WriteString(w, "pane login complete. you can close this window.")
		default:
			http.Error(w, "login already received", http.StatusConflict)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	show("open this url in a browser on this machine to sign in:\n'" + u.String() + "'")
	loginCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	select {
	case code := <-codeCh:
		return m.exchange(loginCtx, code, verifier, redirect)
	case <-loginCtx.Done():
		return loginCtx.Err()
	}
}

func (m *Manager) LoginDevice(ctx context.Context, show Prompter) error {
	body, err := dd.UnbindJSON(deviceStartRequest{ClientID: ClientID})
	if err != nil {
		return errors.New("invalid device request")
	}
	resp, err := m.postJSON(ctx, m.endpoints.deviceStart, body)
	if err != nil {
		return err
	}
	raw, err := readAuthBody(resp)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("device login is unavailable")
	}
	var start deviceStartResponse
	if resp.StatusCode != http.StatusOK || dd.BindJSON(&start, raw) != nil || start.DeviceAuthID == "" || start.UserCode == "" {
		return fmt.Errorf("device login request failed (status %d)", resp.StatusCode)
	}
	interval := parsePollInterval(start.Extra["interval"])
	duration := 15 * time.Minute
	if start.ExpiresIn > 0 && start.ExpiresIn < int(duration/time.Second) {
		duration = time.Duration(start.ExpiresIn) * time.Second
	}
	loginCtx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	show("visit '" + DeviceVerifyURL + "' and enter code '" + start.UserCode + "'")
	for {
		timer := time.NewTimer(interval)
		select {
		case <-loginCtx.Done():
			timer.Stop()
			return loginCtx.Err()
		case <-timer.C:
		}
		body, err := dd.UnbindJSON(devicePollRequest{DeviceAuthID: start.DeviceAuthID, UserCode: start.UserCode})
		if err != nil {
			return errors.New("invalid device poll request")
		}
		resp, err := m.postJSON(loginCtx, m.endpoints.devicePoll, body)
		if err != nil {
			return err
		}
		raw, err := readAuthBody(resp)
		if err != nil {
			return err
		}
		switch resp.StatusCode {
		case http.StatusOK:
			var done devicePollResponse
			if dd.BindJSON(&done, raw) != nil || done.AuthorizationCode == "" || done.CodeVerifier == "" {
				return errors.New("device auth response missing fields")
			}
			return m.exchange(loginCtx, done.AuthorizationCode, done.CodeVerifier, DeviceRedirectURI)
		case http.StatusForbidden, http.StatusNotFound:
			continue
		default:
			var failure deviceErrorResponse
			_ = dd.BindJSON(&failure, raw)
			if failure.Error == "authorization_pending" {
				continue
			}
			if failure.Error == "slow_down" {
				interval += 5 * time.Second
				continue
			}
			return fmt.Errorf("device auth failed (status %d)", resp.StatusCode)
		}
	}
}

func parsePollInterval(value any) time.Duration {
	if number, ok := value.(float64); ok && number > 0 && number <= 60 {
		return time.Duration(number * float64(time.Second))
	}
	if text, ok := value.(string); ok {
		if n, err := strconv.ParseFloat(text, 64); err == nil && n > 0 && n <= 60 {
			return time.Duration(n * float64(time.Second))
		}
	}
	return 5 * time.Second
}

func readAuthBody(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if err != nil || len(raw) > 64*1024 {
		return nil, errors.New("auth response is invalid")
	}
	return raw, nil
}

func (m *Manager) postJSON(ctx context.Context, target string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid auth destination")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New("auth request failed")
	}
	return resp, nil
}

func (m *Manager) exchange(ctx context.Context, code, verifier, redirect string) error {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {ClientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}}
	c, err := m.tokenRequest(ctx, form)
	if err != nil {
		return err
	}
	return m.store.Save(ctx, c)
}
