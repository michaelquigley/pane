package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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
			// a device exchange need not return a refresh token.
			_, _ = w.Write(tokenJSON("acct-pane-test-a", ""))
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
