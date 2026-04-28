package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// Embedded build of the dashboard SPA. The frontend lives under web/ and is
// built (Vite) into web/dist/. We embed the dist/ subtree so a single Go
// binary ships the whole UI.
//
//go:embed web/dist
var distFS embed.FS

// distRoot returns the embedded fs scoped to the dist/ subtree so paths
// resolve to "/index.html" rather than "/web/dist/index.html".
func distRoot() fs.FS {
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
