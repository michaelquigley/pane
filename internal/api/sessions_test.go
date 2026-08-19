package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaelquigley/pane/internal/session"
)

func newSessionsServer(t *testing.T, dir string) *httptest.Server {
	t.Helper()

	store, err := session.NewStore(dir)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	api := &API{sessions: store}
	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func do(t *testing.T, server *httptest.Server, method, path, body string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	return string(body)
}

func errorMessage(t *testing.T, resp *http.Response) string {
	t.Helper()
	var payload map[string]string
	if err := json.Unmarshal([]byte(readBody(t, resp)), &payload); err != nil {
		t.Fatalf("decoding error body: %v", err)
	}
	return payload["error"]
}

func TestSessionsCRUDRoundTrip(t *testing.T) {
	server := newSessionsServer(t, t.TempDir())

	resp := do(t, server, "GET", "/api/sessions", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing an empty store, got %d", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, `"sessions": []`) {
		t.Fatalf("expected an empty sessions array, got %q", body)
	}

	doc := `{"title":"first","messages":[{"role":"user","content":"hi"}],"createdAt":1,"updatedAt":10}`
	resp = do(t, server, "PUT", "/api/sessions/abc", doc)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 saving, got %d", resp.StatusCode)
	}

	resp = do(t, server, "GET", "/api/sessions/abc", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting, got %d", resp.StatusCode)
	}
	if body := readBody(t, resp); body != doc {
		t.Fatalf("expected the stored bytes unchanged, got %q", body)
	}

	putSession(t, server, "zed", `{"title":"second","createdAt":2,"updatedAt":20}`)

	resp = do(t, server, "GET", "/api/sessions", "")
	var listed struct {
		Sessions []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			CreatedAt int64  `json:"createdAt"`
			UpdatedAt int64  `json:"updatedAt"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(readBody(t, resp)), &listed); err != nil {
		t.Fatalf("decoding list: %v", err)
	}
	if len(listed.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(listed.Sessions))
	}
	if listed.Sessions[0].ID != "zed" || listed.Sessions[1].ID != "abc" {
		t.Fatalf("expected updated-descending order, got %+v", listed.Sessions)
	}
	if listed.Sessions[0].Title != "second" || listed.Sessions[0].CreatedAt != 2 || listed.Sessions[0].UpdatedAt != 20 {
		t.Fatalf("expected the projection filled from the body, got %+v", listed.Sessions[0])
	}

	resp = do(t, server, "DELETE", "/api/sessions/abc", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 deleting, got %d", resp.StatusCode)
	}
	resp = do(t, server, "DELETE", "/api/sessions/abc", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 deleting again, got %d", resp.StatusCode)
	}
}

func TestGetMissingSessionIs404(t *testing.T) {
	server := newSessionsServer(t, t.TempDir())

	resp := do(t, server, "GET", "/api/sessions/nope", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
	if message := errorMessage(t, resp); !strings.Contains(message, "nope") {
		t.Fatalf("expected the error to name the id, got %q", message)
	}
}

func TestGetCorruptSessionIs500(t *testing.T) {
	dir := t.TempDir()
	server := newSessionsServer(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "damaged.json"), []byte(`{"title":`), 0o600); err != nil {
		t.Fatalf("writing damaged file: %v", err)
	}

	resp := do(t, server, "GET", "/api/sessions/damaged", "")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if message := errorMessage(t, resp); !strings.Contains(message, "damaged") {
		t.Fatalf("expected the error to name the id, got %q", message)
	}
}

func TestSaveRejectsInvalidDocumentsWith400(t *testing.T) {
	server := newSessionsServer(t, t.TempDir())

	cases := map[string]string{
		"array":          `[1,2,3]`,
		"bare string":    `"nope"`,
		"duplicate keys": `{"title":"a","title":"b"}`,
		"trailing data":  `{"title":"a"} {}`,
		"invalid json":   `{"title":`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := do(t, server, "PUT", "/api/sessions/abc", body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", resp.StatusCode)
			}
			if errorMessage(t, resp) == "" {
				t.Fatalf("expected an error message")
			}
		})
	}
}

func TestSaveOversizedDocumentIs413(t *testing.T) {
	server := newSessionsServer(t, t.TempDir())

	body := `{"title":"` + strings.Repeat("x", session.MaxDocumentSize) + `"}`
	resp := do(t, server, "PUT", "/api/sessions/abc", body)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", resp.StatusCode)
	}
}

func TestOperationalStoreFailureIs500(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write into a read-only directory")
	}

	dir := t.TempDir()
	server := newSessionsServer(t, dir)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// a disk that will not take the write is a server fault, never a client
	// error: the body here is a perfectly valid document.
	resp := do(t, server, "PUT", "/api/sessions/abc", `{"title":"valid"}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
	if errorMessage(t, resp) == "" {
		t.Fatalf("expected the error to name the failure")
	}
}

func TestURLReservedIDRoundTripsEncoded(t *testing.T) {
	dir := t.TempDir()
	server := newSessionsServer(t, dir)

	doc := `{"title":"issue 1","updatedAt":4}`
	putSession(t, server, "issue%231", doc)
	if _, err := os.Stat(filepath.Join(dir, "issue#1.json")); err != nil {
		t.Fatalf("expected the decoded name on disk: %v", err)
	}

	resp := do(t, server, "GET", "/api/sessions/issue%231", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if body := readBody(t, resp); body != doc {
		t.Fatalf("expected the stored bytes, got %q", body)
	}

	resp = do(t, server, "DELETE", "/api/sessions/issue%231", "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestUnsafeIDIs400(t *testing.T) {
	server := newSessionsServer(t, t.TempDir())

	// '%2F' decodes to a separator the store's id rule refuses; the mux
	// itself never routes an undecoded slash to the item handlers.
	resp := do(t, server, "GET", "/api/sessions/sub%2Fdir", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func putSession(t *testing.T, server *httptest.Server, id, doc string) {
	t.Helper()
	resp := do(t, server, "PUT", "/api/sessions/"+id, doc)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 saving '%s', got %d: %s", id, resp.StatusCode, readBody(t, resp))
	}
}
