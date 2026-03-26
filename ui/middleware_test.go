package ui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewStaticHandlerPassesThroughAPIRequests(t *testing.T) {
	handler := newStaticHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), testStaticFS(t))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected api request to pass through, got %d", recorder.Code)
	}
}

func TestNewStaticHandlerServesExistingAsset(t *testing.T) {
	handler := newStaticHandler(http.NotFoundHandler(), testStaticFS(t))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected asset to be served, got %d", recorder.Code)
	}

	if !strings.Contains(recorder.Body.String(), "console.log") {
		t.Fatalf("expected asset content, got %q", recorder.Body.String())
	}
}

func TestNewStaticHandlerReturnsNotFoundForMissingAsset(t *testing.T) {
	handler := newStaticHandler(http.NotFoundHandler(), testStaticFS(t))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected missing asset to return 404, got %d", recorder.Code)
	}
}

func TestNewStaticHandlerServesIndexForSPARoute(t *testing.T) {
	handler := newStaticHandler(http.NotFoundHandler(), testStaticFS(t))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/chat/123", nil)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected spa route to return index, got %d", recorder.Code)
	}

	if !strings.Contains(recorder.Body.String(), "<div id=\"root\"></div>") {
		t.Fatalf("expected index content, got %q", recorder.Body.String())
	}
}

func testStaticFS(t *testing.T) fs.FS {
	t.Helper()

	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!doctype html><html><body><div id=\"root\"></div></body></html>"),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("console.log('pane')"),
		},
	}
}
