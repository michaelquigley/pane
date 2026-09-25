package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserPKCEAndState(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	var tokenCalls atomic.Int32
	verifiers := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		tokenCalls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "good-code" {
			t.Errorf("bad code exchange")
		}
		verifier := r.Form.Get("code_verifier")
		if verifier == "" {
			t.Errorf("missing verifier")
		}
		verifiers <- verifier
		_, _ = w.Write(tokenJSON("acct-pane-test-a", "rotated"))
	}))
	defer server.Close()
	manager := newManager(store, fakeTransport(server.URL), productionEndpoints)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	redirect := "http://" + address + "/auth/callback"
	shown := make(chan string, 1)
	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { result <- manager.loginBrowserAt(ctx, func(s string) { shown <- s }, address, redirect) }()
	var message string
	select {
	case message = <-shown:
	case <-ctx.Done():
		t.Fatal("no authorization url")
	}
	link := strings.Trim(strings.TrimPrefix(message, "open this url in a browser on this machine to sign in:\n"), "'")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "https" || u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" || q.Get("originator") != "pane" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != redirect {
		t.Fatalf("authorization url shape: %s", u.Host+u.Path)
	}
	bad := redirect + "?state=wrong&code=bad-code"
	resp, err := http.Get(bad)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || tokenCalls.Load() != 0 {
		t.Fatal("bad state accepted")
	}
	resp, err = http.Get(redirect + "?state=" + q.Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatal("missing code accepted")
	}
	resp, err = http.Get(redirect + "?state=" + q.Get("state") + "&code=good-code")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal("valid callback rejected")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token calls: %d", tokenCalls.Load())
	}
	got, err := store.Read()
	if err != nil || got.AccountID != "acct-pane-test-a" {
		t.Fatalf("login not stored: %v", err)
	}
	verifier := <-verifiers
	sum := sha256.Sum256([]byte(verifier))
	if q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(sum[:]) {
		t.Fatal("invalid pkce challenge")
	}
}

func TestDevicePendingAndExchange(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = io.WriteString(w, `{"device_auth_id":"device-canary","user_code":"ABCD","interval":0.001,"expires_in":2}`)
		case "/api/accounts/deviceauth/token":
			if polls.Add(1) == 1 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = io.WriteString(w, `{"authorization_code":"auth-code","code_verifier":"verifier"}`)
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "auth-code" || r.Form.Get("redirect_uri") != DeviceRedirectURI {
				t.Error("invalid device exchange")
			}
			_, _ = w.Write(tokenJSON("acct-pane-test-a", "refresh"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	m := newManager(store, fakeTransport(server.URL), productionEndpoints)
	var shown string
	if err := m.LoginDevice(context.Background(), func(s string) { shown = s }); err != nil {
		t.Fatal(err)
	}
	if polls.Load() != 2 || !strings.Contains(shown, "ABCD") || !strings.Contains(shown, DeviceVerifyURL) {
		t.Fatalf("device flow: polls=%d show=%q", polls.Load(), shown)
	}
	if _, err := store.Read(); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceCancellationAndExpiry(t *testing.T) {
	for _, mode := range []string{"cancel", "expire"} {
		t.Run(mode, func(t *testing.T) {
			store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
			if err != nil {
				t.Fatal(err)
			}
			var polls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/accounts/deviceauth/usercode" {
					_, _ = io.WriteString(w, `{"device_auth_id":"id","user_code":"code","interval":0.1,"expires_in":1}`)
					return
				}
				polls.Add(1)
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			m := newManager(store, fakeTransport(server.URL), productionEndpoints)
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "cancel" {
				defer cancel()
			}
			start := time.Now()
			err = m.LoginDevice(ctx, func(string) {
				if mode == "cancel" {
					cancel()
				}
			})
			if err == nil {
				t.Fatal("expected interruption")
			}
			if mode == "cancel" && (polls.Load() != 0 || time.Since(start) > time.Second) {
				t.Fatalf("cancellation delayed or polled: %d", polls.Load())
			}
			if mode == "expire" && time.Since(start) < time.Second {
				t.Fatal("device code did not respect expiry")
			}
		})
	}
}

func TestPollIntervalParsing(t *testing.T) {
	for _, tt := range []struct {
		value any
		want  time.Duration
	}{{float64(5), 5 * time.Second}, {"2", 2 * time.Second}, {float64(0), 5 * time.Second}, {float64(999), 5 * time.Second}} {
		if got := parsePollInterval(tt.value); got != tt.want {
			t.Fatalf("%v: %v", tt.value, got)
		}
	}
}
