package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/repostore"
)

// repoView is one enriched repository for the overview grid. It unions a
// registered repository (if any) with what run history and the catalog know
// about that repo path.
type repoView struct {
	repostore.Repo
	Registered   bool   `json:"registered"`
	RunCount     int    `json:"run_count"`
	LastRunID    string `json:"last_run_id,omitempty"`
	LastStatus   string `json:"last_status,omitempty"`
	LastFinished any    `json:"last_finished_at,omitempty"`
	NodeCount    int    `json:"node_count"`
	EdgeCount    int    `json:"edge_count"`
	PendingCount int    `json:"pending_count"`
}

// repositories returns the union of registered repos and repos discovered from
// run history, enriched with latest deterministic run facts.
func (s *Server) repositories() ([]repoView, error) {
	registered, err := s.repos.List()
	if err != nil {
		return nil, err
	}
	byID := map[string]*repoView{}
	order := []string{}
	ensure := func(path string) *repoView {
		path = filepath.Clean(path)
		id := repostore.IDForPath(path)
		if v, ok := byID[id]; ok {
			return v
		}
		v := &repoView{Repo: repostore.Repo{ID: id, Path: path, Name: filepath.Base(path)}}
		byID[id] = v
		order = append(order, id)
		return v
	}

	for _, r := range registered {
		v := ensure(r.Path)
		v.Repo = r
		v.Registered = true
	}

	// Walk runs once: group by repo path, count, and track the latest run.
	ids, err := s.listRuns()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		sum := s.summarizeRun(id)
		path, _ := sum["repo_path"].(string)
		if strings.TrimSpace(path) == "" {
			continue
		}
		v := ensure(path)
		v.RunCount++
		// Runs are listed newest-first (run_id is a sortable timestamp), so the
		// first one seen for a repo is its latest.
		if v.LastStatus == "" {
			v.LastRunID = id
			if st, ok := sum["status"].(string); ok {
				v.LastStatus = st
			}
			if fin, ok := sum["finished_at"]; ok {
				v.LastFinished = fin
			}
		}
	}

	out := make([]repoView, 0, len(order))
	for _, id := range order {
		v := byID[id]
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RunCount != out[j].RunCount {
			return out[i].RunCount > out[j].RunCount // most-active repos first
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (s *Server) handleRepos(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		repos, err := s.repositories()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]any{"repos": repos})
	case http.MethodPost:
		var req struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
			Name     string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		req.Path = strings.TrimSpace(req.Path)
		if req.Path == "" || !filepath.IsAbs(req.Path) {
			writeErr(w, http.StatusBadRequest, errors.New("path must be absolute"))
			return
		}
		if info, err := os.Stat(req.Path); err != nil || !info.IsDir() {
			writeErr(w, http.StatusBadRequest, errors.New("path must be an existing directory"))
			return
		}
		if fp := strings.TrimSpace(req.FilePath); fp != "" && !filepath.IsAbs(fp) {
			writeErr(w, http.StatusBadRequest, errors.New("file_path must be absolute"))
			return
		}
		saved, err := s.repos.Upsert(repostore.Repo{Path: req.Path, FilePath: req.FilePath, Name: req.Name})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, saved)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleReposItem(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	if id == "" || strings.Contains(id, "/") {
		writeErr(w, http.StatusBadRequest, errors.New("invalid repo id"))
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.repos.Delete(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
