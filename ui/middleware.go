package ui

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func Middleware(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			handler.ServeHTTP(w, r)
			return
		}

		staticPath := "dist"
		indexPath := "index.html"

		path, err := filepath.Abs(r.URL.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		path = filepath.Join(staticPath, path)

		_, err = FS.Open(path)
		if os.IsNotExist(err) {
			index, err := FS.ReadFile(filepath.Join(staticPath, indexPath))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(index)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if statics, err := fs.Sub(FS, staticPath); err == nil {
			http.FileServer(http.FS(statics)).ServeHTTP(w, r)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}
