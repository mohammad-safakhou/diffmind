package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// findSPAWorkspace walks up from cwd looking for a directory that
// contains `internal/ui/web/package.json`. When found it returns the
// path to that web/ directory. When not found it returns "" — that
// means we're running from an installed binary outside the project
// tree, and the caller should fall back to the embedded SPA bundle.
func findSPAWorkspace() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for dir := cwd; ; {
		candidate := filepath.Join(dir, "internal", "ui", "web", "package.json")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Dir(candidate)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// spaRebuildNeeded returns true when ANY file under the SPA source
// tree (src/, index.html, vite.config.js, package.json,
// package-lock.json) has been modified more recently than the OLDEST
// file inside dist/. We use the oldest because if dist/ contains
// stale files alongside a few recently-rebuilt ones the bundle is
// still incomplete and we should rebuild.
//
// Returns true and a human-readable reason when a rebuild is needed,
// false and "" otherwise.
func spaRebuildNeeded(webDir string) (bool, string) {
	distDir := filepath.Join(webDir, "dist")
	if _, err := os.Stat(distDir); err != nil {
		return true, "dist/ does not exist"
	}
	oldestDist, ok := oldestMtimeIn(distDir)
	if !ok {
		return true, "dist/ is empty"
	}
	// Sources we care about. node_modules is intentionally excluded —
	// vite picks up package.json changes via its own dep watcher and
	// scanning node_modules' mtime is huge and irrelevant.
	srcRoots := []string{
		filepath.Join(webDir, "src"),
		filepath.Join(webDir, "index.html"),
		filepath.Join(webDir, "vite.config.js"),
		filepath.Join(webDir, "package.json"),
	}
	for _, p := range srcRoots {
		newest, why, ok := newestMtimeIn(p)
		if !ok {
			continue
		}
		if newest.After(oldestDist) {
			return true, fmt.Sprintf("%s (modified %s) is newer than dist (oldest %s)",
				why,
				newest.Format(time.RFC3339),
				oldestDist.Format(time.RFC3339))
		}
	}
	return false, ""
}

// newestMtimeIn returns the latest modification time found anywhere
// under root (or root itself if it's a file). It returns the path of
// the newest file for nicer error messages. ok=false means root
// doesn't exist.
func newestMtimeIn(root string) (time.Time, string, bool) {
	info, err := os.Stat(root)
	if err != nil {
		return time.Time{}, "", false
	}
	if !info.IsDir() {
		return info.ModTime(), root, true
	}
	var best time.Time
	var bestPath string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.ModTime().After(best) {
			best = fi.ModTime()
			bestPath = path
		}
		return nil
	})
	if bestPath == "" {
		return info.ModTime(), root, true
	}
	return best, bestPath, true
}

// oldestMtimeIn returns the earliest modification time found anywhere
// under root. Used to determine whether dist/ is fully fresh or just
// partially regenerated. ok=false means root is empty.
func oldestMtimeIn(root string) (time.Time, bool) {
	var oldest time.Time
	any := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if !any || fi.ModTime().Before(oldest) {
			oldest = fi.ModTime()
			any = true
		}
		return nil
	})
	return oldest, any
}

// ensureSPABuilt runs `npm run build` in webDir when the SPA sources
// are newer than dist/. Output streams to the user's terminal so they
// see what's happening. Returns the dist path on success; returns
// "" with no error when no workspace is available (we should fall
// back to embedded) or when npm is unavailable (we log a warning and
// keep going with the embedded bundle).
//
// The function is deliberately tolerant: any unrecoverable failure
// degrades to "use the embedded bundle" rather than refusing to
// start the dashboard. Better to serve a slightly stale SPA than to
// be hard-down because npm is missing on the user's machine.
func ensureSPABuilt(skip bool) string {
	webDir := findSPAWorkspace()
	if webDir == "" {
		// No source tree on disk → installed binary. Embedded bundle
		// is authoritative; nothing to do.
		return ""
	}
	distDir := filepath.Join(webDir, "dist")
	if skip {
		util.Info("cli.ui", "spa rebuild skipped via flag", map[string]any{"web_dir": webDir})
		return distDir
	}
	need, reason := spaRebuildNeeded(webDir)
	if !need {
		util.Info("cli.ui", "spa is fresh; no rebuild needed", map[string]any{"web_dir": webDir})
		return distDir
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "[diffmind] SPA sources are newer than dist/ but npm is not on PATH.")
		fmt.Fprintln(os.Stderr, "[diffmind] Install Node + npm, or run `npm run build` once manually under", webDir)
		util.Warn("cli.ui", "npm not on PATH; serving stale SPA", map[string]any{"reason": reason})
		return distDir
	}
	fmt.Fprintln(os.Stderr, "[diffmind] SPA rebuild required:", reason)
	fmt.Fprintln(os.Stderr, "[diffmind] Running `npm run build` in", webDir)
	started := time.Now()
	cmd := exec.Command(npm, "run", "build")
	cmd.Dir = webDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, "[diffmind] npm run build failed; serving stale SPA bundle.")
		}
		util.Warn("cli.ui", "spa rebuild failed; falling back to whatever is in dist/", map[string]any{"error": err})
		return distDir
	}
	fmt.Fprintln(os.Stderr, "[diffmind] SPA rebuilt in", time.Since(started).Round(time.Millisecond))
	util.Info("cli.ui", "spa rebuilt", map[string]any{"web_dir": webDir, "elapsed_ms": time.Since(started).Milliseconds()})
	return distDir
}
