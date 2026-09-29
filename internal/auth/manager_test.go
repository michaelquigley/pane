package auth

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func fakeJWT(account string) string {
	payload, _ := json.Marshal(map[string]any{"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": account}})
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// tokenJSON is a token response; an empty refresh omits the field.
func tokenJSON(account, refresh string) []byte {
	body := map[string]any{"access_token": fakeJWT(account), "expires_in": 3600}
	if refresh != "" {
		body["refresh_token"] = refresh
	}
	b, _ := json.Marshal(body)
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
