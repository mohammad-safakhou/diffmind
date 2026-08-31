package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

// Embedded build of the dashboard SPA. The frontend lives under web/ and is
// built (Vite) into web/dist/. We embed the dist/ subtree so a single Go
// binary ships the whole UI.
//
//go:embed web/dist
var distFS embed.FS

// distOverride is the absolute path to an on-disk dist/ directory that
// takes priority over the embedded bundle when set. Used in dev: the
// CLI's `ui` subcommand discovers `internal/ui/web/dist/` relative to
// the project root and points us at it, so SPA changes are picked up
// on browser reload without restarting the Go process.
//
// nil pointer = use embedded bundle. Atomic because the CLI sets it
// once at startup before any request can land.
var distOverride atomic.Pointer[string]

// SetDistOverride points the static handler at a directory on disk
// instead of the embedded bundle. Passing "" clears the override.
// Safe to call from any goroutine.
func SetDistOverride(path string) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		distOverride.Store(nil)
		return
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		util.Warn("ui.static", "failed to absolutise dist override", map[string]any{"path": clean, "error": err})
		return
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		util.Warn("ui.static", "dist override is not a directory; ignoring", map[string]any{"path": abs, "error": err})
		distOverride.Store(nil)
		return
	}
	distOverride.Store(&abs)
	util.Info("ui.static", "serving dashboard from on-disk dist", map[string]any{"path": abs})
}

// distRoot returns the fs to serve the dashboard from. When an
// on-disk dist override is set we use that (dev mode); otherwise we
// fall back to the embedded bundle (production binaries).
//
// We re-resolve on every request so SPA rebuilds during dev are
// picked up on the next HTTP hit without needing to restart Go.
func distRoot() fs.FS {
	if p := distOverride.Load(); p != nil {
		return os.DirFS(*p)
	}
	root, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		// embed at compile time guarantees this can't fail.
		return distFS
	}
	return root
}

// handleStatic serves the SPA bundle. Anything that isn't a known asset
// falls through to index.html so Preact router-style deep links work.
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

	// Try the exact path first; if it doesn't exist, fall back to index.html
	// so client-side routing works.
	if f, err := root.Open(path); err == nil {
		defer f.Close()
		// Re-open via http.FS for proper Content-Type / Range handling.
		http.FileServer(http.FS(root)).ServeHTTP(w, r)
		return
	}
	if r.URL.Path != "/" && strings.Contains(r.URL.Path, ".") {
		http.NotFound(w, r)
		return
	}
	// SPA fallback.
	indexBytes, err := fs.ReadFile(root, "index.html")
	if err != nil {
		// Dev fallback when no build is present.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(noBuildHTML))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexBytes)
}

// noBuildHTML is shown when the embedded dist/ is empty (e.g. when running
// `go test` against a fresh checkout that hasn't run `make web`). It points
// the developer at the right command to bootstrap.
const noBuildHTML = `<!doctype html>
<html><head><meta charset="utf-8"><title>DiffMind Dashboard</title>
<style>body{font-family:system-ui;background:#0b1222;color:#e5e7eb;padding:48px;line-height:1.6}
code{background:#1f2937;padding:2px 6px;border-radius:4px}</style></head>
<body>
<h1>DiffMind Dashboard</h1>
<p>The web bundle has not been built yet. From the project root run:</p>
<pre>cd internal/ui/web &amp;&amp; npm install &amp;&amp; npm run build</pre>
<p>Then restart the dashboard. The API endpoints under <code>/api/...</code>
are already up; you can drive a run using curl while you set this up.</p>
</body></html>`
