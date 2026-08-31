package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Embedded build of the DiffMind SPA. The frontend lives under web/ and is
// built (Vite) into web/dist/. We embed the dist/ subtree so a single Go
// binary ships the whole UI.
//
//go:embed web/dist
var distFS embed.FS

// distOverride points the static handler at an on-disk dist/ directory (dev
// mode) instead of the embedded bundle. nil = use embedded.
var distOverride atomic.Pointer[string]

var staticLog = util.NewLogger(util.LevelInfo)

// SetDistOverride serves the SPA from a directory on disk instead of the
// embedded bundle. Passing "" clears the override.
func SetDistOverride(path string) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		distOverride.Store(nil)
		return
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		distOverride.Store(nil)
		return
	}
	distOverride.Store(&abs)
	staticLog.Info("serving SPA from on-disk dist", "path", abs)
}

func distRoot() fs.FS {
	if p := distOverride.Load(); p != nil {
		return os.DirFS(*p)
	}
	root, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		return distFS
	}
	return root
}

// handleStatic serves the SPA bundle, falling back to index.html for unknown
// non-asset paths so client-side routing deep links work on refresh.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	root := distRoot()
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if f, err := root.Open(path); err == nil {
		f.Close()
		http.FileServer(http.FS(root)).ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/" && strings.Contains(r.URL.Path, ".") {
		http.NotFound(w, r)
		return
	}
	indexBytes, err := fs.ReadFile(root, "index.html")
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(noBuildHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexBytes)
}

const noBuildHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>DiffMind</title>
<style>body{font-family:system-ui;background:#0b1222;color:#e5e7eb;padding:48px;line-height:1.6}
code{background:#1f2937;padding:2px 6px;border-radius:4px}</style></head>
<body><h1>DiffMind</h1>
<p>The web bundle has not been built yet. From the project root run:</p>
<pre>cd internal/ui/web &amp;&amp; npm install &amp;&amp; npm run build</pre>
<p>The API endpoints under <code>/api/...</code> are already up.</p></body></html>`
