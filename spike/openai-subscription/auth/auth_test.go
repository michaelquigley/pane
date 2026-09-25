package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fakeJWT(account string, n int) string {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	claims, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": account}, "n": n})
	return h + "." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

type tokenServer struct {
	*httptest.Server
	hits    atomic.Int32
	status  atomic.Int32
	account string
}

func newTokenServer(t *testing.T, account string) *tokenServer {
	ts := &tokenServer{account: account}
	ts.status.Store(200)
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := ts.hits.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("client_id") != ClientID {
			w.WriteHeader(400)
			return
		}
		if s := int(ts.status.Load()); s != 200 {
			w.WriteHeader(s)
			_, _ = fmt.Fprintf(w, `{"error":"invalid_grant","echo":"%s"}`, r.Form.Get("refresh_token"))
			return
		}
		time.Sleep(20 * time.Millisecond) // widen the race window
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  fakeJWT(ts.account, int(n)),
			"refresh_token": fmt.Sprintf("REFRESH-%d", n),
			"expires_in":    3600,
		})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func setup(t *testing.T, ts *tokenServer, expires time.Duration) (*Store, *Manager) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	store, err := OpenStore(filepath.Join(t.TempDir(), "scratch"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), &Credential{Access: fakeJWT("acct-1", 0), Refresh: "REFRESH-0", ExpiresAt: time.Now().Add(expires), AccountID: "acct-1"}); err != nil {
		t.Fatal(err)
	}
	m := newManager(store, nil, ts.URL+"/oauth/token")
	return store, m
}

func TestStoreRefusesForeignLocations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, p := range []string{".codex", ".pi/agent", ".config/pane", ".config/pi"} {
		if _, err := OpenStore(filepath.Join(home, p)); err == nil {
			t.Fatalf("store opened inside '%s'", p)
		}
	}
}

func TestStorePermissionsAndRedaction(t *testing.T) {
	ts := newTokenServer(t, "acct-1")
	store, _ := setup(t, ts, time.Hour)
	fi, _ := os.Stat(store.Dir())
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode().Perm())
	}
	fi, _ = os.Stat(store.path())
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}
	c, _ := store.Read()
	if s := fmt.Sprintf("%v %+v %s", *c, *c, c); strings.Contains(s, "REFRESH") {
		t.Fatalf("credential formatting leaks: %s", s)
	}
}

func TestFreshTokenNoRefresh(t *testing.T) {
	ts := newTokenServer(t, "acct-1")
	_, m := setup(t, ts, time.Hour)
	tok, acct, err := m.Access(context.Background())
	if err != nil || acct != "acct-1" || tok == "" {
		t.Fatal(err)
	}
	if ts.hits.Load() != 0 {
		t.Fatal("refreshed a fresh token")
	}
}

func TestRefreshRotationSingleFlight(t *testing.T) {
	ts := newTokenServer(t, "acct-1")
	store, m := setup(t, ts, time.Minute) // inside the five-minute window
	// separate managers model separate processes sharing the store.
	m2 := newManager(store, nil, ts.URL+"/oauth/token")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		mm := m
		if i%2 == 1 {
			mm = m2
		}
		go func() {
			defer wg.Done()
			_, _, err := mm.Access(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if ts.hits.Load() != 1 {
		t.Fatalf("expected one refresh, got %d", ts.hits.Load())
	}
	c, _ := store.Read()
	if c.Refresh != "REFRESH-1" || time.Until(c.ExpiresAt) < 50*time.Minute {
		t.Fatal("rotated credential not persisted")
	}
	if m.Status().AccountScope != AccountScope("acct-1") {
		t.Fatal("account scope changed across refresh")
	}
}

func TestRevokedRefreshRequiresLoginAndKeepsFile(t *testing.T) {
	ts := newTokenServer(t, "acct-1")
	ts.status.Store(400)
	store, m := setup(t, ts, -time.Minute)
	_, _, err := m.Access(context.Background())
	if !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(err.Error(), "REFRESH-0") {
		t.Fatal("refresh token echoed in error")
	}
	if c, err := store.Read(); err != nil || c.Refresh != "REFRESH-0" {
		t.Fatal("credentials removed or altered after rejection")
	}
}

func TestTransientRefreshFailurePreservesCredentials(t *testing.T) {
	ts := newTokenServer(t, "acct-1")
	ts.status.Store(503)
	store, m := setup(t, ts, -time.Minute)
	_, _, err := m.Access(context.Background())
	var re *RefreshError
	if !errors.As(err, &re) || re.Rejected || errors.Is(err, ErrLoginRequired) {
		t.Fatalf("got %v", err)
	}
	if c, _ := store.Read(); c.Refresh != "REFRESH-0" {
		t.Fatal("credentials altered after transient failure")
	}
	ts.status.Store(200)
	if _, _, err := m.Access(context.Background()); err != nil {
		t.Fatalf("recovery after transient failure: %v", err)
	}
}

func TestRefreshAccountChangeRefused(t *testing.T) {
	ts := newTokenServer(t, "acct-2")
	store, m := setup(t, ts, -time.Minute)
	if _, _, err := m.Access(context.Background()); err == nil {
		t.Fatal("account change accepted")
	}
	if c, _ := store.Read(); c.AccountID != "acct-1" {
		t.Fatal("credentials replaced by another account")
	}
}

func TestLogoutAndSignedOut(t *testing.T) {
	ts := newTokenServer(t, "acct-1")
	store, m := setup(t, ts, time.Hour)
	if err := store.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Access(context.Background()); !errors.Is(err, ErrLoginRequired) {
		t.Fatalf("got %v", err)
	}
	if m.Status().SignedIn {
		t.Fatal("status after logout")
	}
}

func TestAuthClientDestinations(t *testing.T) {
	var hit atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hit.Store(true) }))
	defer other.Close()
	c := lockedAuthClient(nil, TokenURL)
	if _, err := c.Post(other.URL+"/oauth/token", "text/plain", nil); err == nil || hit.Load() {
		t.Fatal("auth client reached a foreign destination")
	}
}

func TestJWTAndScope(t *testing.T) {
	id, err := AccountIDFromJWT(fakeJWT("acct-9", 0))
	if err != nil || id != "acct-9" {
		t.Fatal(err)
	}
	if _, err := AccountIDFromJWT("not.a.jwt"); err == nil {
		t.Fatal("garbage accepted")
	}
	s := AccountScope("acct-9")
	if strings.Contains(s, "acct-9") || !strings.HasPrefix(s, "chatgpt:") || s != AccountScope("acct-9") || s == AccountScope("acct-8") {
		t.Fatalf("scope %q", s)
	}
}
