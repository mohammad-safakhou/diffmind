package ui

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleFsList powers the discovery-file picker: it lists sub-directories and
// YAML files under an absolute path so the SPA can browse to a diffmind.yaml
// without the user pasting a path. This is a localhost dev tool; it does not
// guard against symlink escape (out of scope) but it never panics on a bad path
// and rejects relative/.. paths.
func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" {
		// Seed at the user's home directory.
		if home, err := os.UserHomeDir(); err == nil {
			path = home
		} else {
			path = "/"
		}
	}
	if !filepath.IsAbs(path) {
		writeErr(w, http.StatusBadRequest, errors.New("path must be absolute"))
		return
	}
	path = filepath.Clean(path)
	if strings.Contains(path, "..") {
		writeErr(w, http.StatusBadRequest, errors.New("invalid path"))
		return
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var dirs, files []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // hide dotfiles/dirs by default
		}
		if e.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		if ext := strings.ToLower(filepath.Ext(name)); ext == ".yaml" || ext == ".yml" {
			files = append(files, name)
		}
	}
	sort.Strings(dirs)
	sort.Strings(files)

	parent := filepath.Dir(path)
	if parent == path {
		parent = "" // at filesystem root
	}

	writeJSON(w, map[string]any{
		"path":        path,
		"parent":      parent,
		"dirs":        dirs,
		"files":       files,
		"suggestions": s.repoDirSuggestions(),
	})
}

// repoDirSuggestions returns distinct repo directories seen in run history, so
// the picker can jump straight to known repositories.
func (s *Server) repoDirSuggestions() []string {
	ids, err := s.listRuns()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		path, _ := s.summarizeRun(id)["repo_path"].(string)
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
