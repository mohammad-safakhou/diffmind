package ui

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/preflight"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/util"
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
	checks := preflight.DefaultChecks(preflight.Options{})
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
