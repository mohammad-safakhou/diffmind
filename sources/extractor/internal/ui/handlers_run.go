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

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/runner"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// startRunRequest is the body shape of POST /api/runs. Every field maps 1:1
// to a CLI flag so the form can mirror the existing UX.
type startRunRequest struct {
	RepoPath string `json:"repo_path"`

	OpenCode struct {
		BaseURL      string `json:"base_url"`
		Username     string `json:"username"`
		Password     string `json:"password"`
		ProviderID   string `json:"provider_id"`
		ModelID      string `json:"model_id"`
		ModelVariant string `json:"model_variant"`
		TimeoutSec   int    `json:"timeout_seconds"`
	} `json:"opencode"`

	Runtime struct {
		Workers                 int  `json:"workers"`
		MaxCatalogItems         int  `json:"max_catalog_items"`
		ReuseOpenCodeSession    bool `json:"reuse_opencode_session"`
		CleanupOpenCodeSessions bool `json:"cleanup_opencode_sessions"`
		OpenCodeDeleteDelaySec  int  `json:"opencode_delete_delay_seconds"`
		SkipReexamination       bool `json:"skip_reexamination"`
	} `json:"runtime"`

	Quality struct {
		MinConfidence float64 `json:"min_confidence"`
	} `json:"quality"`
}

// handleRunCreate validates the form payload, builds a config.Config, then
// delegates to the singleton runner. Returns 409 if a run is already in
// progress.
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
	if strings.TrimSpace(req.OpenCode.BaseURL) == "" {
		writeErr(w, http.StatusBadRequest, errors.New("opencode.base_url is required"))
		return
	}

	cfg := config.Default()
	cfg.OpenCode.BaseURL = req.OpenCode.BaseURL
	cfg.OpenCode.Username = req.OpenCode.Username
	cfg.OpenCode.Password = req.OpenCode.Password
	cfg.OpenCode.ProviderID = req.OpenCode.ProviderID
	cfg.OpenCode.ModelID = req.OpenCode.ModelID
	cfg.OpenCode.ModelVariant = req.OpenCode.ModelVariant
	if req.OpenCode.TimeoutSec > 0 {
		cfg.OpenCode.TimeoutSec = req.OpenCode.TimeoutSec
	}
	if req.Runtime.Workers > 0 {
		cfg.Runtime.Workers = req.Runtime.Workers
	}
	if req.Runtime.MaxCatalogItems > 0 {
		cfg.Runtime.MaxCatalogItems = req.Runtime.MaxCatalogItems
	}
	cfg.Runtime.ReuseOpenCodeSession = req.Runtime.ReuseOpenCodeSession
	cfg.Runtime.CleanupOpenCodeSessions = req.Runtime.CleanupOpenCodeSessions
	if req.Runtime.OpenCodeDeleteDelaySec > 0 {
		cfg.Runtime.OpenCodeDeleteDelaySec = req.Runtime.OpenCodeDeleteDelaySec
	}
	cfg.Runtime.SkipReexamination = req.Runtime.SkipReexamination
	if req.Quality.MinConfidence > 0 {
		cfg.Quality.MinConfidence = req.Quality.MinConfidence
	}
	cfg.Artifacts.BaseDir = s.baseDir

	runID, err := s.runner.Start(context.Background(), runner.StartParams{
		RepoPath: repo,
		Config:   cfg,
	})
	if err != nil {
		if errors.Is(err, runner.ErrBusy) {
			writeErr(w, http.StatusConflict, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{"run_id": runID, "status": runner.StatusRunning})
	util.Info("ui.api", "run created", map[string]any{"run_id": runID, "repo": repo})
}

// handleRunCancel cancels the active run if its id matches.
func (s *Server) handleRunCancel(w http.ResponseWriter, _ *http.Request, runID string) {
	st := s.runner.State()
	if st.RunID != runID {
		writeErr(w, http.StatusNotFound, fmt.Errorf("run %s is not active", runID))
		return
	}
	if err := s.runner.Cancel(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"status": runner.StatusCancelling})
}

// handleRunState returns the current state + buffered events. UI uses it for
// cold loads (before opening the SSE stream).
func (s *Server) handleRunState(w http.ResponseWriter, _ *http.Request, runID string) {
	st := s.runner.State()
	snapshot := s.bus.Snapshot(runID)
	writeJSON(w, map[string]any{
		"run_id": runID,
		"state":  st,
		"events": snapshot,
		"counts": map[string]int{"events": len(snapshot)},
	})
}

// handleRunEvents serves a Server-Sent Events stream for the run. It honors
// the Last-Event-ID header (or `from` query param) so reconnects don't
// duplicate events. When the run has finished and no more events are
// expected, the connection is closed cleanly.
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
		// Run might be finished and pruned from the bus; try JSONL replay.
		path := filepath.Join(s.baseDir, runID, "events.jsonl")
		if _, err := os.Stat(path); err == nil {
			s.streamReplay(r.Context(), w, flusher, path, from)
			return
		}
		writeErr(w, http.StatusNotFound, err)
		return
	}
	defer cancel()

	// Initial heartbeat so proxies don't buffer.
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.Kind == "_eof" {
				_, _ = fmt.Fprintf(w, "event: eof\ndata: {}\n\n")
				flusher.Flush()
				return
			}
			writeSSE(w, e)
			flusher.Flush()
		}
	}
}

// streamReplay sends events stored in a JSONL file as SSE messages. Used
// when a run finished long ago and the in-memory ring has expired.
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

// writeSSE serializes an event to the SSE wire format with id + event +
// data fields.
func writeSSE(w http.ResponseWriter, e events.Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\n", e.Seq)
	_, _ = fmt.Fprintf(w, "event: %s\n", e.Kind)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
}

// handleRunJob returns the prompt + response files for a single job inside
// a run. The dashboard uses this for click-to-view detail.
func (s *Server) handleRunJob(w http.ResponseWriter, _ *http.Request, runID, jobID string) {
	if jobID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("job id required"))
		return
	}
	dir := filepath.Join(s.baseDir, runID, "prompts")
	safeJob := safeJobName(jobID)
	prompt := readOptionalFile(filepath.Join(dir, safeJob+".prompt.txt"))
	response := readOptionalFile(filepath.Join(dir, safeJob+".response.json"))
	writeJSON(w, map[string]any{
		"run_id":   runID,
		"job_id":   jobID,
		"prompt":   prompt,
		"response": response,
	})
}

func readOptionalFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// safeJobName mirrors agents.safeJobID without importing the package. We
// only need this for filesystem lookups in the prompt cache.
func safeJobName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "_"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 96 {
		out = out[:96]
	}
	return out
}
