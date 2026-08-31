package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/config"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/events"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/runner"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
)

type startRunRequest struct {
	RepoPath string `json:"repo_path"`
	Runtime  struct {
		Workers int `json:"workers"`
	} `json:"runtime"`
	Quality struct {
		MinConfidence float64 `json:"min_confidence"`
	} `json:"quality"`
}

func buildConfigFromRequest(req startRunRequest) config.Config {
	cfg := config.Default()
	if req.Runtime.Workers > 0 {
		cfg.Runtime.Workers = req.Runtime.Workers
	}
	if req.Quality.MinConfidence > 0 {
		cfg.Quality.MinConfidence = req.Quality.MinConfidence
	}
	return cfg
}

func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	var req startRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
		return
	}
	repo := strings.TrimSpace(req.RepoPath)
	if repo == "" {
		writeErr(w, http.StatusBadRequest, errors.New("repo_path is required"))
		return
	}
	if _, err := os.Stat(repo); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("repo_path is not accessible: %w", err))
		return
	}
	cfg := buildConfigFromRequest(req)
	cfg.Artifacts.BaseDir = s.baseDir

	util.Info("ui.api", "starting deterministic run from dashboard", map[string]any{
		"repo":           repo,
		"pipeline":       cfg.Pipeline(),
		"workers":        cfg.Runtime.Workers,
		"min_confidence": cfg.Quality.MinConfidence,
	})

	runID, err := s.runner.Start(context.Background(), runner.StartParams{
		RepoPath: repo,
		Config:   cfg,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"run_id": runID, "status": runner.StatusRunning})
	util.Info("ui.api", "run created", map[string]any{"run_id": runID, "repo": repo})
}

func (s *Server) handleRunCancel(w http.ResponseWriter, _ *http.Request, runID string) {
	if err := s.runner.Cancel(runID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"run_id": runID, "status": runner.StatusCancelling})
}

func (s *Server) handleRunDelete(w http.ResponseWriter, _ *http.Request, runID string) {
	if strings.TrimSpace(runID) == "" || strings.Contains(runID, "/") || strings.Contains(runID, "..") {
		writeErr(w, http.StatusBadRequest, errors.New("invalid run id"))
		return
	}
	if st, ok := s.runner.State(runID); ok && (st.Status == runner.StatusRunning || st.Status == runner.StatusCancelling) {
		_ = s.runner.Cancel(runID)
		s.runner.WaitFor(runID)
	}
	runDir := filepath.Join(s.baseDir, runID)
	if _, err := os.Stat(runDir); err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, fmt.Errorf("run %s not found", runID))
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := os.RemoveAll(runDir); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("delete run: %w", err))
		return
	}
	util.Info("ui.api", "run deleted", map[string]any{"run_id": runID, "run_dir": runDir})
	writeJSON(w, map[string]any{"run_id": runID, "deleted": true})
}

func (s *Server) handleRunState(w http.ResponseWriter, _ *http.Request, runID string) {
	st, ok := s.runner.State(runID)
	if !ok {
		st = runner.State{RunID: runID, Status: s.diskStatus(runID)}
	}
	snapshot := s.bus.Snapshot(runID)
	writeJSON(w, map[string]any{
		"run_id": runID,
		"state":  st,
		"events": snapshot,
		"counts": map[string]int{"events": len(snapshot)},
	})
}

func (s *Server) handleAggregateEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, cancel := s.runner.SubscribeLifecycle()
	defer cancel()

	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
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

func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request, runID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	from := uint64(0)
	if v := strings.TrimSpace(r.Header.Get("Last-Event-ID")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			from = n + 1
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("from")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			from = n
		}
	}

	ch, cancel, err := s.bus.Subscribe(runID, from, 1024)
	if err != nil {
		path := filepath.Join(s.baseDir, runID, "events.jsonl")
		if _, err := os.Stat(path); err == nil {
			s.streamReplay(r.Context(), w, flusher, path, from)
			return
		}
		writeErr(w, http.StatusNotFound, err)
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
			if !ok || e.Kind == "_eof" {
				_, _ = fmt.Fprintf(w, "event: eof\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			writeSSE(w, e)
			flusher.Flush()
		}
	}
}

func (s *Server) streamReplay(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, path string, from uint64) {
	out := make(chan events.Event, 64)
	done := make(chan error, 1)
	go func() {
		done <- events.ReplayJSONL(ctx, path, out)
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-out:
			if !ok {
				return
			}
			if e.Seq < from {
				continue
			}
			writeSSE(w, e)
			flusher.Flush()
		case err := <-done:
			if err != nil {
				util.Warn("ui.api", "replay error", map[string]any{"error": err})
			}
			_, _ = fmt.Fprintf(w, "event: eof\ndata: {}\n\n")
			flusher.Flush()
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, e events.Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\n", e.Seq)
	_, _ = fmt.Fprintf(w, "event: %s\n", e.Kind)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

func (s *Server) handleRunJob(w http.ResponseWriter, _ *http.Request, runID, jobID string) {
	if jobID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("job id required"))
		return
	}
	writeJSON(w, map[string]any{
		"run_id": runID,
		"job_id": jobID,
	})
}
