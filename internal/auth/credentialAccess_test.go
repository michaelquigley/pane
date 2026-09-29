package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// refusingTransport counts and refuses every request: credential access and
// status must never reach the network.
type refusingTransport struct{ count atomic.Int32 }

func (r *refusingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.count.Add(1)
	return nil, errors.New("network refused in test")
}

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestNewCredentialFileOmitsRefresh(t *testing.T) {
	store := newStore(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_, _ = io.WriteString(w, `{"device_auth_id":"id","user_code":"CODE","interval":0.001,"expires_in":2}`)
		case "/api/accounts/deviceauth/token":
			_, _ = io.WriteString(w, `{"authorization_code":"auth-code","code_verifier":"verifier"}`)
		case "/oauth/token":
			// a provider refresh token in the response is ignored.
			_, _ = w.Write(tokenJSON("acct-pane-test-a", "refresh-canary"))
		}
	}))
	defer server.Close()
	m := newManager(store, fakeTransport(server.URL), productionEndpoints)
	if err := m.LoginDevice(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.path())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["access"] == nil || fields["expires_at"] == nil || fields["account_id"] != "acct-pane-test-a" {
		t.Fatalf("credential file fields: %v", keys(fields))
	}
	if bytes.Contains(raw, []byte("refresh")) {
		t.Fatal("new credential file carries a refresh token")
	}
}

func keys(fields map[string]any) []string {
	var out []string
	for key := range fields {
		out = append(out, key)
	}
	return out
}

// writeLegacy writes a credential file in the earlier format, with a
// refresh token, exactly as a previous build would have left it.
func writeLegacy(t *testing.T, store *Store, expires time.Time) []byte {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"access": fakeJWT("acct-pane-test-a"), "refresh": "legacy-refresh-canary",
		"expires_at": expires.UTC().Format(time.RFC3339Nano), "account_id": "acct-pane-test-a"})
	if err := os.WriteFile(store.path(), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLegacyRefreshFieldIsReadButNeverUsedOrRewritten(t *testing.T) {
	for _, tt := range []struct {
		name    string
		expires time.Duration
		usable  bool
	}{{"unexpired", time.Hour, true}, {"expired", -time.Hour, false}} {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore(t)
			original := writeLegacy(t, store, time.Now().Add(tt.expires))
			before, _ := os.Stat(store.path())
			network := &refusingTransport{}
			m := newManager(store, network, productionEndpoints)

			status, err := m.Status()
			if err != nil || status.SignedIn != tt.usable || status.Expired == tt.usable {
				t.Fatalf("status = %+v, %v", status, err)
			}
			access, account, err := m.Access(context.Background())
			if tt.usable {
				if err != nil || access != fakeJWT("acct-pane-test-a") || account != "acct-pane-test-a" {
					t.Fatalf("legacy access: %v", err)
				}
			} else if !errors.Is(err, ErrLoginRequired) {
				t.Fatalf("expired legacy credential: %v", err)
			}
			if network.count.Load() != 0 {
				t.Fatal("reading a legacy credential reached the network")
			}
			after, _ := os.Stat(store.path())
			raw, _ := os.ReadFile(store.path())
			if !bytes.Equal(raw, original) || !after.ModTime().Equal(before.ModTime()) {
				t.Fatal("reading rewrote the legacy credential file")
			}
		})
	}
}

func TestExpiryBoundaryRequiresLogin(t *testing.T) {
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name   string
		offset time.Duration
		usable bool
	}{{"past", -time.Second, false}, {"equal", 0, false}, {"future", time.Second, true}} {
		t.Run(tt.name, func(t *testing.T) {
			store := newStore(t)
			if err := store.Save(context.Background(), &Credential{Access: fakeJWT("acct-pane-test-a"), AccountID: "acct-pane-test-a", ExpiresAt: now.Add(tt.offset)}); err != nil {
				t.Fatal(err)
			}
			network := &refusingTransport{}
			m := newManager(store, network, productionEndpoints)
			m.now = func() time.Time { return now }

			status, err := m.Status()
			if err != nil || status.SignedIn != tt.usable || status.Expired == tt.usable || status.ExpiryMarker == "" {
				t.Fatalf("status = %+v, %v", status, err)
			}
			_, accountErr := m.CurrentAccount(context.Background())
			_, accessErr := m.AccessForAccount(context.Background(), "acct-pane-test-a")
			for _, err := range []error{accountErr, accessErr} {
				if tt.usable && err != nil || !tt.usable && !errors.Is(err, ErrLoginRequired) {
					t.Fatalf("usable=%v err=%v", tt.usable, err)
				}
			}
			if network.count.Load() != 0 {
				t.Fatal("credential access reached the network")
			}
		})
	}
}

func TestSignedOutStatusAndAccessAreLocal(t *testing.T) {
	store := newStore(t)
	network := &refusingTransport{}
	m := newManager(store, network, productionEndpoints)
	if status, err := m.Status(); err != nil || status.SignedIn || status.Expired || status.ExpiryMarker != "" {
		t.Fatalf("signed-out status = %+v, %v", status, err)
	}
	if _, err := m.CurrentAccount(context.Background()); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("signed-out account: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := m.Access(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled access: %v", err)
	}
	if network.count.Load() != 0 {
		t.Fatal("signed-out access reached the network")
	}
}

func TestSameAccountReplacementAndAccountChange(t *testing.T) {
	store := newStore(t)
	m := newManager(store, &refusingTransport{}, productionEndpoints)
	save := func(account, access string) {
		t.Helper()
		if err := store.Save(context.Background(), &Credential{Access: access, AccountID: account, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	save("acct-pane-test-a", "token-a1")
	if got, err := m.AccessForAccount(context.Background(), "acct-pane-test-a"); err != nil || got != "token-a1" {
		t.Fatalf("first login: %v", err)
	}
	// a same-account relogin replaces the token without disturbing the binding.
	save("acct-pane-test-a", "token-a2")
	if got, err := m.AccessForAccount(context.Background(), "acct-pane-test-a"); err != nil || got != "token-a2" {
		t.Fatalf("same-account relogin: %v", err)
	}
	save("acct-pane-test-b", "token-b")
	if _, err := m.AccessForAccount(context.Background(), "acct-pane-test-a"); err == nil || !strings.Contains(err.Error(), "account changed") {
		t.Fatalf("different account accepted mid-turn: %v", err)
	}
}
