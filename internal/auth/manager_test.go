package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fakeJWT(account string) string {
	payload, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func tokenJSON(account, refresh string) []byte {
	b, _ := json.Marshal(map[string]any{"access_token": fakeJWT(account), "refresh_token": refresh, "expires_in": 3600})
	return b
}

func fakeTransport(serverURL string) http.RoundTripper {
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(serverURL, "http://")
		clone.Host = clone.URL.Host
		return http.DefaultTransport.RoundTrip(clone)
	})
}

func TestRefreshRotationAndAccountGuard(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), &Credential{Access: fakeJWT("acct-pane-test-a"), Refresh: "old-refresh-canary", AccountID: "acct-pane-test-a", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		if r.URL.Path != "/oauth/token" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		_ = r.ParseForm()
		if r.PostForm.Get("refresh_token") != "old-refresh-canary" {
			t.Errorf("wrong refresh token")
		}
		_, _ = w.Write(tokenJSON("acct-pane-test-a", "new-refresh-canary"))
	}))
	defer server.Close()
	m := newManager(store, fakeTransport(server.URL), productionEndpoints)
	access, account, err := m.Access(context.Background())
	if err != nil || access != fakeJWT("acct-pane-test-a") || account != "acct-pane-test-a" {
		t.Fatalf("access: %v %q", err, account)
	}
	if _, _, err := m.Access(context.Background()); err != nil || count.Load() != 1 {
		t.Fatalf("duplicate refresh: %v count %d", err, count.Load())
	}
	if got, err := store.Read(); err != nil || got.Refresh != "new-refresh-canary" {
		t.Fatalf("rotation not persisted: %v %v", got, err)
	}
	if _, err := m.AccessForAccount(context.Background(), "acct-pane-test-b"); err == nil {
		t.Fatal("accepted different pending account")
	}
}

func TestRefreshRejectsAccountChangeAndPreservesCredential(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	old := &Credential{Access: fakeJWT("acct-pane-test-a"), Refresh: "old", AccountID: "acct-pane-test-a", ExpiresAt: time.Now().Add(-time.Minute)}
	if err := store.Save(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tokenJSON("acct-pane-test-b", "new")) }))
	defer server.Close()
	m := newManager(store, fakeTransport(server.URL), productionEndpoints)
	if _, _, err := m.Access(context.Background()); err == nil {
		t.Fatal("accepted account-changing refresh")
	}
	got, err := store.Read()
	if err != nil || got.Refresh != "old" {
		t.Fatalf("credential replaced: %v %v", got, err)
	}
}

func TestDestinationLockAndRedirect(t *testing.T) {
	var foreign atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreign.Add(1); w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	client := lockedClient(http.DefaultTransport, TokenURL)
	for _, target := range []string{server.URL, TokenURL + "?refresh_token=canary", "https://example.com/oauth/token"} {
		req, _ := http.NewRequest(http.MethodPost, target, strings.NewReader("refresh_token=canary"))
		if _, err := client.Do(req); err == nil || strings.Contains(err.Error(), "canary") {
			t.Fatalf("unsafe request/error: %v", err)
		}
	}
	if foreign.Load() != 0 {
		t.Fatal("foreign destination received request")
	}
	client = lockedClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{server.URL}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}), TokenURL)
	req, _ := http.NewRequest(http.MethodPost, TokenURL, strings.NewReader("refresh_token=canary"))
	if _, err := client.Do(req); err == nil || foreign.Load() != 0 {
		t.Fatalf("redirect escaped: %v", err)
	}
	gen := NewGenerationClient(http.DefaultTransport)
	req, _ = http.NewRequest(http.MethodPost, server.URL, strings.NewReader("bearer-canary"))
	req.Header.Set("Authorization", "Bearer bearer-canary")
	if _, err := gen.Do(req); err == nil || foreign.Load() != 0 {
		t.Fatalf("generation escaped: %v", err)
	}
}

func TestAuthProcessHelper(t *testing.T) {
	if os.Getenv("PANE_AUTH_TEST_CHILD") != "1" {
		return
	}
	store, err := OpenStore(os.Getenv("PANE_AUTH_TEST_DIR"))
	if err != nil {
		t.Fatal(err)
	}
	m := newManager(store, fakeTransport(os.Getenv("PANE_AUTH_TEST_SERVER")), productionEndpoints)
	fmt.Println("ready")
	var line string
	if _, err := fmt.Fscanln(os.Stdin, &line); err != nil || line != "go" {
		t.Fatalf("start signal: %v", err)
	}
	_, _, err = m.Access(context.Background())
	if err != nil {
		t.Fatal(err)
	}
}

func TestCrossProcessSingleRefreshAndLogout(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pane"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), &Credential{Access: fakeJWT("acct-pane-test-a"), Refresh: "old", AccountID: "acct-pane-test-a", ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		_, _ = w.Write(tokenJSON("acct-pane-test-a", "rotated"))
	}))
	defer server.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		cmd *exec.Cmd
		in  io.WriteCloser
		out *bytes.Buffer
	}
	children := make([]child, 2)
	for i := range children {
		cmd := exec.Command(executable, "-test.run=^TestAuthProcessHelper$")
		cmd.Env = append(os.Environ(), "PANE_AUTH_TEST_CHILD=1", "PANE_AUTH_TEST_DIR="+store.dir, "PANE_AUTH_TEST_SERVER="+server.URL)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out := &bytes.Buffer{}
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children[i] = child{cmd, stdin, out}
	}
	for i := range children {
		_, _ = io.WriteString(children[i].in, "go\n")
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not start")
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := store.Logout(context.Background()); err != nil {
			t.Errorf("logout: %v", err)
		}
	}()
	close(release)
	for _, child := range children {
		if err := child.cmd.Wait(); err != nil && !strings.Contains(child.out.String(), "login required") {
			t.Errorf("child: %v: %s", err, child.out.String())
		}
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatalf("expected one refresh, got %d", count.Load())
	}
	if _, err := store.Read(); err != ErrLoginRequired {
		t.Fatalf("logout did not persist: %v", err)
	}
}
