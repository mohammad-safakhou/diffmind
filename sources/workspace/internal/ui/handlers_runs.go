package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/store"
)

// handleDiffMindRuns implements GET /api/diffmind-runs[?repo_path=…] (G5).
func (s *Server) handleDiffMindRuns(w http.ResponseWriter, r *http.Request) {
	repoPath := r.URL.Query().Get("repo_path")
	if repoPath != "" {
		groups, err := artifacts.DiscoverDiffMindRunsByRepo(s.diffmindRunsDir)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"runs": groups[repoPath]})
		return
	}
	groups, err := artifacts.DiscoverDiffMindRunsByRepo(s.diffmindRunsDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	all, err := artifacts.DiscoverDiffMindRuns(s.diffmindRunsDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": all, "by_repo": groups})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.store.ListRuns(r.PathValue("pid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

type createRunRequest struct {
	Repos   []store.RunRepoRef `json:"repos"`
	Options map[string]any     `json:"options"`
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("pid")
	if _, err := s.store.GetProject(pid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var req createRunRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Repos) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("at least one repo is required"))
		return
	}
	run, err := s.runs.Start(pid, req.Repos, req.Options)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.GetRun(r.PathValue("pid"), r.PathValue("rid"))
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "active": s.runs.IsActive(r.PathValue("pid"), r.PathValue("rid"))})
}

func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	s.runs.Cancel(r.PathValue("pid"), r.PathValue("rid"))
	writeJSON(w, http.StatusOK, map[string]any{"status": store.RunCancelling})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	// Cancel any in-flight work and wait for the goroutine to release the
	// directory before deleting.
	if s.runs.IsActive(pid, rid) {
		s.runs.Cancel(pid, rid)
		s.runs.WaitFor(pid, rid)
	}
	if err := s.store.DeleteRun(pid, rid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// handleRunGraph serves the persisted graph.json for a finished run.
func (s *Server) handleRunGraph(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	if _, err := s.store.GetRun(pid, rid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.store.RunDir(pid, rid), "graph.json"))
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, errors.New("graph not available yet"))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// handleRunEvents streams run progress over SSE.
func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	pid, rid := r.PathValue("pid"), r.PathValue("rid")
	if _, err := s.store.GetRun(pid, rid); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel, err := s.runs.Subscribe(pid, rid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	defer cancel()

	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				_, _ = fmt.Fprintf(w, "event: eof\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			b, err := json.Marshal(e)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
			flusher.Flush()
		}
	}
}
