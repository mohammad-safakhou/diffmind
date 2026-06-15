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

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/preflight"
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
		PromptRetryCount        *int `json:"prompt_retry_count"`
		// Liveness watchdog knobs. 0 = use config default. See
		// config.Runtime for field semantics.
		IdleTimeoutSec  int `json:"idle_timeout_seconds"`
		MaxCallSeconds  int `json:"max_call_seconds"`
		LivenessPollSec int `json:"liveness_poll_seconds"`
		// Discovery-strengthening knobs. See config.Runtime for semantics;
		// mode/samples use the "empty/0 = config default" convention.
		DiscoveryVerify         bool   `json:"discovery_verify"`
		DiscoveryVerifyMode     string `json:"discovery_verify_mode"`
		DiscoveryVerifySamples  int    `json:"discovery_verify_samples"`
		DiscoveryFrameworkScope bool   `json:"discovery_framework_scope"`
	} `json:"runtime"`

	Quality struct {
		MinConfidence float64 `json:"min_confidence"`
	} `json:"quality"`
}

// buildConfigFromRequest translates a startRunRequest into a
// config.Config. It is a PURE function (no I/O, no Runtime.BaseDir
// override) so it is easy to unit-test the field-mapping logic
// without spinning up a server. The "0 means use server default"
// convention applies to every numeric field — the SPA sends 0 from
// inputs the user has not explicitly populated, and we MUST NOT
// overwrite a sensible default (e.g. 4-hour transport timeout) with
// it. See run 20260518T113418Z for the cautionary tale: the SPA was
// sending timeout_seconds: 300, which silently clobbered the 4-hour
// fail-safe and reintroduced the 300-second wall that the liveness
// watchdog was supposed to replace.
func buildConfigFromRequest(req startRunRequest) config.Config {
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
	cfg.Runtime.DiscoveryVerify = req.Runtime.DiscoveryVerify
	if req.Runtime.DiscoveryVerifyMode != "" {
		cfg.Runtime.DiscoveryVerifyMode = req.Runtime.DiscoveryVerifyMode
	}
	if req.Runtime.DiscoveryVerifySamples > 0 {
		cfg.Runtime.DiscoveryVerifySamples = req.Runtime.DiscoveryVerifySamples
	}
	cfg.Runtime.DiscoveryFrameworkScope = req.Runtime.DiscoveryFrameworkScope
	if req.Runtime.PromptRetryCount != nil {
		cfg.Runtime.PromptRetryCount = *req.Runtime.PromptRetryCount
	}
	if req.Runtime.IdleTimeoutSec > 0 {
		cfg.Runtime.IdleTimeoutSec = req.Runtime.IdleTimeoutSec
	}
	if req.Runtime.MaxCallSeconds > 0 {
		cfg.Runtime.MaxCallSeconds = req.Runtime.MaxCallSeconds
	}
	if req.Runtime.LivenessPollSec > 0 {
		cfg.Runtime.LivenessPollSec = req.Runtime.LivenessPollSec
	}
	if req.Quality.MinConfidence > 0 {
		cfg.Quality.MinConfidence = req.Quality.MinConfidence
	}
	return cfg
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

	cfg := buildConfigFromRequest(req)
	cfg.Artifacts.BaseDir = s.baseDir

	// Hard-rejection gate: run a synchronous preflight against
	// the EFFECTIVE config we are about to launch with. We
	// deliberately do not trust the cached /api/preflight Report
	// here — the user may have edited the form between the
	// last ticker fire and the Run click, and a stale Report
	// could let a misconfigured run through.
	//
	// Any SeverityFail aborts the request with 422 + a payload
	// listing every failed check so the UI can surface clear
	// remediation. Warnings are allowed through.
	checks := preflight.DefaultChecks(preflight.OptionsFromConfig(cfg))
	rep := preflight.NewRunner(checks).Run(r.Context())
	if rep.HasFail() {
		failures := rep.Failures()
		// Build a single-line summary for the legacy `error` field
		// + the full structured payload for the new UI.
		var brief strings.Builder
		brief.WriteString("preflight rejected the run: ")
		for i, f := range failures {
			if i > 0 {
				brief.WriteString("; ")
			}
			brief.WriteString(f.Title + " - " + f.Message)
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(w, map[string]any{
			"error":     brief.String(),
			"preflight": rep,
		})
		util.Warn("ui.api", "run rejected by preflight", map[string]any{
			"repo":     repo,
			"failures": len(failures),
		})
		return
	}

	// Log the effective config the dashboard is about to launch. We
	// learned the hard way (runs 20260518T113418Z, 20260518T115925Z)
	// that a stale localStorage value can silently set TimeoutSec=300
	// and bypass every other safeguard. With this log line the next
	// such regression is one grep away.
	util.Info("ui.api", "starting run from dashboard", map[string]any{
		"repo":                           repo,
		"opencode_transport_timeout_sec": cfg.OpenCode.TimeoutSec,
		"idle_timeout_sec":               cfg.Runtime.IdleTimeoutSec,
		"prompt_retry_count":             cfg.Runtime.PromptRetryCount,
		"max_call_sec":                   cfg.Runtime.MaxCallSeconds,
		"liveness_poll_sec":              cfg.Runtime.LivenessPollSec,
		"workers":                        cfg.Runtime.Workers,
		"max_catalog_items":              cfg.Runtime.MaxCatalogItems,
		"skip_reexamination":             cfg.Runtime.SkipReexamination,
		"discovery_verify":               cfg.Runtime.DiscoveryVerify,
		"discovery_verify_mode":          cfg.Runtime.DiscoveryVerifyMode,
		"discovery_framework_scope":      cfg.Runtime.DiscoveryFrameworkScope,
		"reuse_session":                  cfg.Runtime.ReuseOpenCodeSession,
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

// retryRequest is the body of POST /api/runs/{id}/retry. Every field
// is optional; when omitted the existing run's manifest provides the
// repo path and we fall back to default credentials. The typical
// retry case is "I switched OpenCode accounts after the run failed
// on an expired token" — for that the user supplies a fresh
// opencode.password (and optionally provider/model).
type retryRequest struct {
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
		Workers              int  `json:"workers"`
		MaxCatalogItems      int  `json:"max_catalog_items"`
		IdleTimeoutSec       int  `json:"idle_timeout_seconds"`
		PromptRetryCount     *int `json:"prompt_retry_count"`
		MaxCallSeconds       int  `json:"max_call_seconds"`
		LivenessPollSec      int  `json:"liveness_poll_seconds"`
		ReuseOpenCodeSession bool `json:"reuse_opencode_session"`
		SkipReexamination    bool `json:"skip_reexamination"`

		DiscoveryVerify         bool   `json:"discovery_verify"`
		DiscoveryVerifyMode     string `json:"discovery_verify_mode"`
		DiscoveryVerifySamples  int    `json:"discovery_verify_samples"`
		DiscoveryFrameworkScope bool   `json:"discovery_framework_scope"`
	} `json:"runtime"`
}

// handleRunRetry resumes a previously-failed run. It only requires
// the run id (in the URL). The optional JSON body lets the caller
// override OpenCode credentials for the retry, which is the path
// the dashboard's Retry button uses when the original failure was an
// auth or quota issue.
func (s *Server) handleRunRetry(w http.ResponseWriter, r *http.Request, runID string) {
	// Optional body. Empty body is a valid no-override retry.
	var req retryRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid json body: %w", err))
			return
		}
	}

	// Build the retry config: start from the current server config
	// (loaded from disk on server startup), then layer any overrides
	// from the request on top. Note that this is NOT the original
	// run's config — that lives on disk inside the manifest, but
	// app.RetryRun rereads what it needs from there. What we pass
	// here only controls OpenCode connectivity / liveness for the
	// retry attempt itself.
	cfg := config.Default()
	if req.OpenCode.BaseURL != "" {
		cfg.OpenCode.BaseURL = req.OpenCode.BaseURL
	}
	if req.OpenCode.Username != "" {
		cfg.OpenCode.Username = req.OpenCode.Username
	}
	if req.OpenCode.Password != "" {
		cfg.OpenCode.Password = req.OpenCode.Password
	}
	if req.OpenCode.ProviderID != "" {
		cfg.OpenCode.ProviderID = req.OpenCode.ProviderID
	}
	if req.OpenCode.ModelID != "" {
		cfg.OpenCode.ModelID = req.OpenCode.ModelID
	}
	if req.OpenCode.ModelVariant != "" {
		cfg.OpenCode.ModelVariant = req.OpenCode.ModelVariant
	}
	if req.OpenCode.TimeoutSec > 0 {
		cfg.OpenCode.TimeoutSec = req.OpenCode.TimeoutSec
	}
	if req.Runtime.Workers > 0 {
		cfg.Runtime.Workers = req.Runtime.Workers
	}
	if req.Runtime.MaxCatalogItems > 0 {
		cfg.Runtime.MaxCatalogItems = req.Runtime.MaxCatalogItems
	}
	if req.Runtime.IdleTimeoutSec > 0 {
		cfg.Runtime.IdleTimeoutSec = req.Runtime.IdleTimeoutSec
	}
	if req.Runtime.PromptRetryCount != nil {
		cfg.Runtime.PromptRetryCount = *req.Runtime.PromptRetryCount
	}
	if req.Runtime.MaxCallSeconds > 0 {
		cfg.Runtime.MaxCallSeconds = req.Runtime.MaxCallSeconds
	}
	if req.Runtime.LivenessPollSec > 0 {
		cfg.Runtime.LivenessPollSec = req.Runtime.LivenessPollSec
	}
	cfg.Runtime.ReuseOpenCodeSession = req.Runtime.ReuseOpenCodeSession
	cfg.Runtime.SkipReexamination = req.Runtime.SkipReexamination
	cfg.Runtime.DiscoveryVerify = req.Runtime.DiscoveryVerify
	if req.Runtime.DiscoveryVerifyMode != "" {
		cfg.Runtime.DiscoveryVerifyMode = req.Runtime.DiscoveryVerifyMode
	}
	if req.Runtime.DiscoveryVerifySamples > 0 {
		cfg.Runtime.DiscoveryVerifySamples = req.Runtime.DiscoveryVerifySamples
	}
	cfg.Runtime.DiscoveryFrameworkScope = req.Runtime.DiscoveryFrameworkScope
	cfg.Artifacts.BaseDir = s.baseDir

	// Hard-rejection: retries face the same preflight gate as fresh
	// runs. The whole point is to never enqueue a run we know will
	// fail (Docker down, OpenCode unreachable, etc.).
	{
		checks := preflight.DefaultChecks(preflight.OptionsFromConfig(cfg))
		rep := preflight.NewRunner(checks).Run(r.Context())
		if rep.HasFail() {
			failures := rep.Failures()
			var brief strings.Builder
			brief.WriteString("preflight rejected the retry: ")
			for i, f := range failures {
				if i > 0 {
					brief.WriteString("; ")
				}
				brief.WriteString(f.Title + " - " + f.Message)
			}
			w.WriteHeader(http.StatusUnprocessableEntity)
			writeJSON(w, map[string]any{
				"error":     brief.String(),
				"preflight": rep,
			})
			util.Warn("ui.api", "retry rejected by preflight", map[string]any{
				"run_id":   runID,
				"failures": len(failures),
			})
			return
		}
	}

	util.Info("ui.api", "retrying run from dashboard", map[string]any{
		"run_id":                         runID,
		"opencode_transport_timeout_sec": cfg.OpenCode.TimeoutSec,
		"idle_timeout_sec":               cfg.Runtime.IdleTimeoutSec,
		"prompt_retry_count":             cfg.Runtime.PromptRetryCount,
		"max_call_sec":                   cfg.Runtime.MaxCallSeconds,
		"liveness_poll_sec":              cfg.Runtime.LivenessPollSec,
	})

	id, err := s.runner.Retry(context.Background(), runner.RetryParams{
		RunID:  runID,
		Config: cfg,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"run_id": id, "status": runner.StatusRunning})
}

// handleRunCancel cancels a single run by id. Cancelling an unknown or
// already-finished run is a no-op that still returns 200 so the UI can treat
// the button as idempotent.
func (s *Server) handleRunCancel(w http.ResponseWriter, _ *http.Request, runID string) {
	if err := s.runner.Cancel(runID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"run_id": runID, "status": runner.StatusCancelling})
}

// handleRunDelete removes a run and its artifacts from disk. If the run is
// still active it is cancelled first. The UI is responsible for showing a
// confirmation dialog before issuing this request.
func (s *Server) handleRunDelete(w http.ResponseWriter, _ *http.Request, runID string) {
	if strings.TrimSpace(runID) == "" || strings.Contains(runID, "/") || strings.Contains(runID, "..") {
		writeErr(w, http.StatusBadRequest, errors.New("invalid run id"))
		return
	}
	// Cancel any in-flight work, then wait for the goroutine to release the
	// run directory before deleting it.
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

// handleRunState returns the current state + buffered events. UI uses it for
// cold loads (before opening the SSE stream).
func (s *Server) handleRunState(w http.ResponseWriter, _ *http.Request, runID string) {
	st, ok := s.runner.State(runID)
	if !ok {
		// Fall back to the on-disk summary so a finished run that predates
		// this process still reports a meaningful status.
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

// handleAggregateEvents streams run lifecycle events (created/started/finished)
// over SSE for the homepage dashboard.
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
				// Bus closed the subscription; signal EOF so the SPA
				// can flip the run status from "running" to its real
				// terminal state.
				_, _ = fmt.Fprintf(w, "event: eof\ndata: {}\n\n")
				flusher.Flush()
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
