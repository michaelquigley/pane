package ui

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

func Middleware(handler http.Handler) http.Handler {
	staticFS, err := fs.Sub(FS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		})
	}
	return newStaticHandler(handler, staticFS)
}

func newStaticHandler(apiHandler http.Handler, staticFS fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(staticFS))
	indexHTML, indexErr := fs.ReadFile(staticFS, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}

		cleanPath := cleanAssetPath(r.URL.Path)
		if cleanPath == "" {
			serveIndex(w, indexHTML, indexErr)
			return
		}

		if path.Ext(cleanPath) == "" {
			serveIndex(w, indexHTML, indexErr)
			return
		}

		if _, err := fs.Stat(staticFS, cleanPath); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

func cleanAssetPath(requestPath string) string {
	cleanPath := path.Clean("/" + requestPath)
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	if cleanPath == "." {
		return ""
	}
	return cleanPath
}

func serveIndex(w http.ResponseWriter, indexHTML []byte, indexErr error) {
	if indexErr != nil {
		http.Error(w, indexErr.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(indexHTML)
}
