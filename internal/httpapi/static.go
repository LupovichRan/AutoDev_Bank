package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// staticHandler serves the built web app. GET/HEAD requests for an existing
// file get that file; extension-less paths that are not files fall back to
// index.html (the SPA shell); anything else is 404. The shell and the
// service worker are revalidated on every load so a deploy is picked up at
// once; Vite's content-hashed /assets/ are cached for a year.
func staticHandler(fsys fs.FS) http.Handler {
	if fsys == nil {
		fsys = emptyFS{}
	}
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			Write(w, r, http.StatusMethodNotAllowed, "", "method_not_allowed", nil)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, fsys)
			return
		}
		if st, err := fs.Stat(fsys, name); err == nil && !st.IsDir() {
			switch {
			case name == "sw.js" || name == "manifest.webmanifest":
				w.Header().Set("Cache-Control", "no-cache")
			case strings.HasPrefix(name, "assets/"):
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if strings.Contains(path.Base(name), ".") {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, fsys)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
