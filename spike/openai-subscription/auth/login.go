package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// Prompter shows login instructions to the operator. it receives urls and
// user codes, never tokens.
type Prompter func(format string, args ...any)

func pkce() (verifier, challenge string, err error) {
	var b [32]byte
	if _, err = rand.Read(b[:]); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

// LoginBrowser runs the authorization-code + PKCE flow with a loopback
// callback on 127.0.0.1:1455, matching the registered redirect uri.
func (m *Manager) LoginBrowser(ctx context.Context, originator string, show Prompter) error {
	verifier, challenge, err := pkce()
	if err != nil {
		return err
	}
	var sb [16]byte
	if _, err := rand.Read(sb[:]); err != nil {
		return err
	}
	state := hex.EncodeToString(sb[:])

	u, _ := url.Parse(AuthorizeURL)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", ClientID)
	q.Set("redirect_uri", RedirectURI)
	q.Set("scope", Scope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	q.Set("originator", originator)
	u.RawQuery = q.Encode()

	ln, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return fmt.Errorf("callback port 1455 unavailable: %w", err)
	}
	codeCh := make(chan string, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/callback" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "pane spike login complete. you can close this window.")
		select {
		case codeCh <- code:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	show("open this url in a browser on this machine to sign in:\n%s\n", u.String())
	var code string
	select {
	case code = <-codeCh:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Minute):
		return errors.New("login timed out")
	}
	return m.exchange(ctx, code, verifier, RedirectURI)
}

// LoginDevice runs the device-code flow: the operator enters a user code at
// the verification page, possibly on another machine.
func (m *Manager) LoginDevice(ctx context.Context, show Prompter) error {
	body, _ := json.Marshal(map[string]string{"client_id": ClientID})
	resp, err := m.postJSON(ctx, DeviceUserCodeURL, body)
	if err != nil {
		return err
	}
	var start struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("device code login is not enabled for this account/server")
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &start) != nil || start.DeviceAuthID == "" || start.UserCode == "" {
		return fmt.Errorf("device code request failed (status %d)", resp.StatusCode)
	}
	interval := 5 * time.Second
	var n float64
	var s string
	if json.Unmarshal(start.Interval, &n) == nil && n > 0 {
		interval = time.Duration(n * float64(time.Second))
	} else if json.Unmarshal(start.Interval, &s) == nil {
		if d, err := time.ParseDuration(s + "s"); err == nil && d > 0 {
			interval = d
		}
	}

	show("visit %s and enter code: %s\n", DeviceVerifyURL, start.UserCode)
	deadline := time.Now().Add(15 * time.Minute)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
		pb, _ := json.Marshal(map[string]string{"device_auth_id": start.DeviceAuthID, "user_code": start.UserCode})
		resp, err := m.postJSON(ctx, DeviceTokenURL, pb)
		if err != nil {
			return err
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusOK:
			var done struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
			}
			if json.Unmarshal(raw, &done) != nil || done.AuthorizationCode == "" || done.CodeVerifier == "" {
				return errors.New("device auth response missing fields")
			}
			return m.exchange(ctx, done.AuthorizationCode, done.CodeVerifier, DeviceRedirectURI)
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
			continue // pending
		default:
			var e struct {
				Error json.RawMessage `json:"error"`
			}
			_ = json.Unmarshal(raw, &e)
			if bytes.Contains(e.Error, []byte("authorization_pending")) {
				continue
			}
			if bytes.Contains(e.Error, []byte("slow_down")) {
				interval += 5 * time.Second
				continue
			}
			return fmt.Errorf("device auth failed (status %d)", resp.StatusCode)
		}
	}
	return errors.New("device code expired")
}

func (m *Manager) postJSON(ctx context.Context, target string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return m.client.Do(req)
}

func (m *Manager) exchange(ctx context.Context, code, verifier, redirect string) error {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirect},
	}
	c, err := m.tokenRequest(ctx, form)
	if err != nil {
		return err
	}
	return m.store.Save(ctx, c)
}
