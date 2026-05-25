package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/preflight"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// preflightState caches the most recent preflight Report so the
// dashboard's poll endpoint can return it instantly. The state is
// refreshed by a background ticker started in Server.startPreflight().
//
// We expose only the cached snapshot via /api/preflight; forcing a
// fresh probe on every poll would saturate the docker daemon
// (`docker image inspect` calls a few times a second per browser
// tab) and add latency. The ticker is the single point of truth.
type preflightState struct {
	mu       sync.RWMutex
	report   preflight.Report
	hasValue bool
	// formOpts is the most recent OpenCode form values pushed by
	// the SPA via POST /api/preflight/options. We keep them
	// separate from the server's loaded config because the form
	// can override the URL / credentials interactively.
	formOpts preflight.Options
}

// snapshot returns a copy of the cached Report.
func (p *preflightState) snapshot() (preflight.Report, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.report, p.hasValue
}

// setReport replaces the cached Report. Called by the ticker.
func (p *preflightState) setReport(r preflight.Report) {
	p.mu.Lock()
	p.report = r
	p.hasValue = true
	p.mu.Unlock()
}

// setOptions records the latest form-derived Options. The ticker
// reads these when constructing the check set.
func (p *preflightState) setOptions(o preflight.Options) {
	p.mu.Lock()
	p.formOpts = o
	p.mu.Unlock()
}

// options returns a copy of the cached form Options.
func (p *preflightState) options() preflight.Options {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.formOpts
}

// startPreflight kicks off the background refresh loop and returns
// immediately. The loop runs an immediate check, then refreshes
// every PreflightInterval (default 30 s). It stops when ctx is
// cancelled (server shutdown).
//
// The first refresh is synchronous: callers who hit /api/preflight
// immediately after server boot don't get an empty report.
func (s *Server) startPreflight(ctx context.Context) {
	if s.preflight == nil {
		s.preflight = &preflightState{}
	}
	s.refreshPreflight(ctx)
	go func() {
		ticker := time.NewTicker(s.preflightInterval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshPreflight(ctx)
			}
		}
	}()
}

// preflightInterval returns the configured refresh cadence (default 30s).
func (s *Server) preflightInterval() time.Duration {
	if s.preflightTicker > 0 {
		return s.preflightTicker
	}
	return 30 * time.Second
}

// refreshPreflight runs a single preflight cycle and stores the
// result. Always returns; errors during a check become part of the
// Report itself.
func (s *Server) refreshPreflight(ctx context.Context) {
	opts := s.preflight.options()
	checks := preflight.DefaultChecks(opts)
	runner := preflight.NewRunner(checks)
	report := runner.Run(ctx)
	s.preflight.setReport(report)
	util.Trace("ui.preflight", "report refreshed", map[string]any{
		"overall": string(report.Overall),
		"checks":  len(report.Checks),
	})
}

// handlePreflight is the GET handler for /api/preflight. Returns
// the cached Report; falls back to an immediate refresh on first
// hit (before the ticker has had a chance to populate).
func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.preflight == nil {
		s.preflight = &preflightState{}
	}
	rep, ok := s.preflight.snapshot()
	if !ok {
		s.refreshPreflight(r.Context())
		rep, _ = s.preflight.snapshot()
	}
	writeJSON(w, rep)
}

// handlePreflightOptions accepts POST /api/preflight/options with
// the live form values the SPA wants the checks evaluated against.
// We use this so the dashboard's System Status panel reflects the
// URL / credentials the user typed BEFORE pressing Run, not just
// the boot-time defaults.
//
// Body shape mirrors the relevant subset of startRunRequest:
//
//	{
//	  "opencode": { "base_url", "username", "password",
//	                "provider_id", "model_id" }
//	}
//
// A successful POST returns 200 with the latest cached Report (we
// refresh synchronously so the SPA sees the new values immediately).
func (s *Server) handlePreflightOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.preflight == nil {
		s.preflight = &preflightState{}
	}
	var body struct {
		OpenCode struct {
			BaseURL    string `json:"base_url"`
			Username   string `json:"username"`
			Password   string `json:"password"`
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		} `json:"opencode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.preflight.setOptions(preflight.Options{
		OpenCodeURL:  body.OpenCode.BaseURL,
		OpenCodeUser: body.OpenCode.Username,
		OpenCodePass: body.OpenCode.Password,
		ProviderID:   body.OpenCode.ProviderID,
		ModelID:      body.OpenCode.ModelID,
	})
	s.refreshPreflight(r.Context())
	rep, _ := s.preflight.snapshot()
	writeJSON(w, rep)
}
