package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// findSPAWorkspace walks up from cwd looking for internal/ui/web/package.json.
// Returns the web/ directory, or "" when running from an installed binary
// (caller falls back to the embedded bundle).
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

// spaRebuildNeeded reports whether any SPA source is newer than the oldest file
// in dist/.
func spaRebuildNeeded(webDir string) (bool, string) {
	distDir := filepath.Join(webDir, "dist")
	if _, err := os.Stat(distDir); err != nil {
		return true, "dist/ does not exist"
	}
	oldestDist, ok := oldestMtimeIn(distDir)
	if !ok {
		return true, "dist/ is empty"
	}
	for _, p := range []string{
		filepath.Join(webDir, "src"),
		filepath.Join(webDir, "index.html"),
		filepath.Join(webDir, "vite.config.js"),
		filepath.Join(webDir, "package.json"),
	} {
		newest, why, ok := newestMtimeIn(p)
		if !ok {
			continue
		}
		if newest.After(oldestDist) {
			return true, fmt.Sprintf("%s is newer than dist", why)
		}
	}
	return false, ""
}

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
		if walkErr != nil || d.IsDir() {
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

func oldestMtimeIn(root string) (time.Time, bool) {
	var oldest time.Time
	any := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
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

// ensureSPABuilt rebuilds the SPA when sources are newer than dist/. It is
// tolerant: any failure degrades to "use whatever dist/ holds (or embedded)".
func ensureSPABuilt(skip bool) string {
	webDir := findSPAWorkspace()
	if webDir == "" {
		return ""
	}
	distDir := filepath.Join(webDir, "dist")
	if skip {
		return distDir
	}
	need, reason := spaRebuildNeeded(webDir)
	if !need {
		return distDir
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		fmt.Fprintln(os.Stderr, "[diffmind] SPA sources newer than dist/ but npm is not on PATH; serving stale bundle.")
		return distDir
	}
	fmt.Fprintln(os.Stderr, "[diffmind] SPA rebuild required:", reason)
	cmd := exec.Command(npm, "run", "build")
	cmd.Dir = webDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			fmt.Fprintln(os.Stderr, "[diffmind] npm run build failed; serving stale SPA bundle.")
		}
		return distDir
	}
	return distDir
}
