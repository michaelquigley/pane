package auth

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerationStreamOutlivesAuthTimeout(t *testing.T) {
	const shortAuthTimeout = 40 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-time.After(3 * shortAuthTimeout):
			_, _ = io.WriteString(w, "data: second\n\n")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	transport := fakeTransport(server.URL)
	if got := lockedClient(transport, TokenURL).client.Timeout; got != authRequestTimeout {
		t.Fatalf("auth client timeout: %v", got)
	}
	bounded := newLockedClient(transport, shortAuthTimeout, GenerationURL)
	if bounded.client.Timeout != shortAuthTimeout {
		t.Fatal("auth-style client lost its request timeout")
	}
	request, _ := http.NewRequest(http.MethodPost, GenerationURL, nil)
	response, err := bounded.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err == nil {
		t.Fatal("auth-style request outlived its timeout")
	}

	generation := NewGenerationClient(transport)
	if generation.client.Timeout != 0 {
		t.Fatalf("generation inherited a whole-request timeout: %v", generation.client.Timeout)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, _ = http.NewRequestWithContext(ctx, http.MethodPost, GenerationURL, nil)
	response, err = generation.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !strings.Contains(string(body), "data: second") {
		t.Fatalf("healthy generation stream ended early: %v %q", err, body)
	}
	if time.Since(started) <= shortAuthTimeout {
		t.Fatal("stream did not outlive the auth timeout")
	}
}

func TestGenerationStreamStopsOnCallerCancellation(t *testing.T) {
	observedCancellation := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(observedCancellation)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, GenerationURL, nil)
	response, err := NewGenerationClient(fakeTransport(server.URL)).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if line, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil || line != "data: first\n" {
		t.Fatalf("stream did not start: %q %v", line, err)
	}
	cancel()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("cancelled stream continued")
	}
	select {
	case <-observedCancellation:
	case <-time.After(time.Second):
		t.Fatal("provider did not observe cancellation")
	}
}
