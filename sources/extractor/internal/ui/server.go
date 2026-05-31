// Package ui hosts the dashboard's HTTP API and the embedded web app. It
// exposes endpoints for launching/observing/cancelling runs and serves the
// single-page UI built under internal/ui/web.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/runner"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Server hosts the dashboard. It owns:
//   - a single events.Bus shared by every run,
//   - a runner.Runner enforcing one-active-run-at-a-time,
//   - the HTTP API,
//   - the embedded SPA bundle.
//
// The legacy artifact-browser endpoints (/api/runs, /api/run/{id}) are
// preserved so older surfaces keep working.
type Server struct {
	baseDir string
	host    string
	port    int
	bus     *events.Bus
	runner  *runner.Runner
	token   string // optional auth; empty = open

	// preflight caches the most recent system-readiness report
	// and the form-derived Options the SPA last pushed. Populated
	// by startPreflight() before the HTTP listener accepts
	// requests, then refreshed every preflightTicker (default 30s).
	preflight *preflightState
	// preflightTicker overrides the refresh cadence. Tests set
	// this to a tiny value to verify periodic refresh; production
	// uses the 30s default via preflightInterval().
	preflightTicker time.Duration
}

// SetToken protects every endpoint with the given shared secret. Clients
// must present it via the X-DiffMind-Token header, a `token` query param,
// or a `diffmind_token` cookie. Empty disables auth (the default).
func (s *Server) SetToken(token string) { s.token = strings.TrimSpace(token) }

// RunData is the legacy artifact-browser payload, kept for backwards
// compatibility with older clients.
type RunData struct {
	RunID        string                      `json:"run_id"`
	Manifest     model.RunManifest           `json:"manifest"`
	Exposures    map[string][]map[string]any `json:"exposures"`
	Dependencies map[string][]map[string]any `json:"dependencies"`
	Connections  map[string][]map[string]any `json:"connections"`
	Unresolved   map[string][]map[string]any `json:"unresolved"`
	Counts       map[string]map[string]int   `json:"counts"`
}

// New constructs a Server. The bus + runner are created internally; callers
// don't need to wire them.
func New(baseDir, host string, port int) *Server {
	if strings.TrimSpace(baseDir) == "" {
		baseDir = ".diffmind/runs"
	}
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 8080
	}
	bus := events.NewBus(20000)
	r := runner.New(baseDir, bus)
	return &Server{baseDir: baseDir, host: host, port: port, bus: bus, runner: r}
}

// Addr returns "host:port".
func (s *Server) Addr() string { return fmt.Sprintf("%s:%d", s.host, s.port) }

// Runner exposes the singleton run controller (used in tests).
func (s *Server) Runner() *runner.Runner { return s.runner }

// Bus exposes the events bus (used in tests).
func (s *Server) Bus() *events.Bus { return s.bus }

// Start runs the HTTP server until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	// Kick off the preflight ticker BEFORE the HTTP listener
	// accepts requests so /api/preflight is never empty.
	s.startPreflight(ctx)

	mux := http.NewServeMux()
	s.routes(mux)

	var handler http.Handler = mux
	if s.token != "" {
		handler = s.tokenMiddleware(mux)
		util.Info("ui", "ui token auth enabled", nil)
	}

	srv := &http.Server{Addr: s.Addr(), Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		util.Info("ui", "dashboard listening", map[string]any{"addr": s.Addr(), "base_dir": s.baseDir})
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

// tokenMiddleware enforces the optional shared-secret guard. Two carve-outs:
//   - /healthz remains unauthenticated so liveness checks still work,
//   - GET / (the SPA shell) is also unauthenticated so users can land on
//     a login screen / paste their token via the query param. The SPA then
//     stores it client-side and includes it in subsequent requests.
//
// Static assets (the bundle JS/CSS) are likewise allowed since they are
// not sensitive on their own.
func (s *Server) tokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requiresAuth(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.tokenMatches(r) {
			w.Header().Set("WWW-Authenticate", "Bearer realm=diffmind")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requiresAuth(r *http.Request) bool {
	if s.token == "" {
		return false
	}
	p := r.URL.Path
	if p == "/healthz" {
		return false
	}
	// Allow the SPA shell + static assets so the user can land in a browser
	// and paste their token. The /api/* surface is what we actually protect.
	if !strings.HasPrefix(p, "/api/") {
		return false
	}
	return true
}

func (s *Server) tokenMatches(r *http.Request) bool {
	got := r.Header.Get("X-DiffMind-Token")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got == "" {
		if c, err := r.Cookie("diffmind_token"); err == nil {
			got = c.Value
		}
	}
	if got == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			got = strings.TrimPrefix(auth, "Bearer ")
		}
	}
	return got != "" && got == s.token
}

// routes registers every HTTP endpoint on the supplied mux. Pulled out so
// tests can build httptest.Servers cheaply.
func (s *Server) routes(mux *http.ServeMux) {
	// Static SPA.
	mux.HandleFunc("/", s.handleStatic)

	// Health check.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// Live run lifecycle.
	mux.HandleFunc("/api/runs", s.handleRunsCollection)   // GET (list) | POST (create)
	mux.HandleFunc("/api/runs/active", s.handleActiveRun) // GET status of singleton
	mux.HandleFunc("/api/runs/", s.handleRunsItem)        // /{id}/(events|state|cancel|job/...)

	// Preflight / System Status. GET returns the cached Report
	// (refreshed every 30s by the background ticker); POST options
	// pushes form-derived OpenCode URL / credentials so the cached
	// Report reflects what the SPA is about to submit.
	mux.HandleFunc("/api/preflight", s.handlePreflight)
	mux.HandleFunc("/api/preflight/options", s.handlePreflightOptions)

	// Legacy artifact browser.
	mux.HandleFunc("/api/run/", s.handleRun)
}

// handleRunsCollection dispatches between list (GET) and create (POST). For
// GET it returns runs from disk plus an `active` state; for POST it kicks
// off a new run.
func (s *Server) handleRunsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleRunsList(w, r)
	case http.MethodPost:
		s.handleRunCreate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleRunsList responds with runs persisted to disk plus the active run
// state from the singleton Runner. The runs array includes a small summary
// per run (counts, started_at, repo) so the sidebar can render labels
// without forcing a second round-trip per row.
func (s *Server) handleRunsList(w http.ResponseWriter, _ *http.Request) {
	ids, err := s.listRuns()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	summaries := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		summaries = append(summaries, s.summarizeRun(id))
	}
	writeJSON(w, map[string]any{
		"runs":     summaries,
		"run_ids":  ids, // kept for backwards compatibility with the legacy UI
		"active":   s.runner.State(),
		"base_dir": s.baseDir,
	})
}

// summarizeRun returns the sidebar's projection of a run. Status is
// derived from the on-disk artifact layout because we don't keep a
// dedicated terminal-status file: a successful run writes
// run_manifest.json (and never writes run_failure.json); a failed
// or cancelled run writes run_failure.json (and skips the manifest
// until the retry succeeds). Distinguishing failed vs cancelled
// requires reading the failure report (which carries a `cancelled`
// boolean).
//
// Older runs from before the cancelled flag was added have no way
// to tell the two apart and surface as "failed"; that matches the
// CLI's behaviour and is the safer default.
func (s *Server) summarizeRun(id string) map[string]any {
	runDir := filepath.Join(s.baseDir, id)
	out := map[string]any{
		"run_id":     id,
		"has_events": s.hasEventsLog(id),
	}
	manifestPath := filepath.Join(runDir, "run_manifest.json")
	failurePath := filepath.Join(runDir, "run_failure.json")

	// Failure report wins as the source of truth when both files
	// exist (e.g. a retry that succeeded then failed again would
	// leave both behind — we read the failure first to surface the
	// freshest state). When only run_failure.json is present, the
	// run definitely did not complete successfully.
	if fb, err := os.ReadFile(failurePath); err == nil {
		var f struct {
			Stage      string    `json:"stage"`
			ErrorClass string    `json:"error_class"`
			Cancelled  bool      `json:"cancelled"`
			OccurredAt time.Time `json:"occurred_at"`
			Error      string    `json:"error"`
		}
		if json.Unmarshal(fb, &f) == nil {
			out["status"] = "failed"
			if f.Cancelled {
				out["status"] = "cancelled"
			}
			out["failed_stage"] = f.Stage
			out["error_class"] = f.ErrorClass
			out["error"] = f.Error
			if !f.OccurredAt.IsZero() {
				out["finished_at"] = f.OccurredAt
			}
		} else {
			// run_failure.json exists but is unparseable. The run
			// definitely failed; we just can't say more.
			out["status"] = "failed"
		}
	}

	// Manifest is opportunistic: when present it overrides status to
	// "completed" only if no failure report sat next to it (the
	// failure report only appears on non-success paths in the
	// current orchestrator). It also fills in repo_path and counts
	// regardless.
	if b, err := os.ReadFile(manifestPath); err == nil {
		var manifest model.RunManifest
		if err := json.Unmarshal(b, &manifest); err == nil {
			out["repo_path"] = manifest.RepoPath
			out["started_at"] = manifest.StartedAt
			if _, hasFinished := out["finished_at"]; !hasFinished && !manifest.FinishedAt.IsZero() {
				out["finished_at"] = manifest.FinishedAt
			}
			out["counts"] = manifest.Counts
			if !manifest.FinishedAt.IsZero() && !manifest.StartedAt.IsZero() {
				out["duration_ms"] = manifest.FinishedAt.Sub(manifest.StartedAt).Milliseconds()
			}
			if _, hasStatus := out["status"]; !hasStatus {
				out["status"] = "completed"
			}
		}
	}

	// Fallback: directory exists but neither manifest nor failure
	// report was readable. Treat as unknown — the sidebar will show
	// the run id but won't claim it succeeded.
	if _, hasStatus := out["status"]; !hasStatus {
		out["status"] = "unknown"
	}
	return out
}

func (s *Server) hasEventsLog(id string) bool {
	_, err := os.Stat(filepath.Join(s.baseDir, id, "events.jsonl"))
	return err == nil
}

// handleActiveRun returns the singleton runner state.
func (s *Server) handleActiveRun(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.runner.State())
}

// handleRunsItem dispatches /api/runs/{id}/* paths.
func (s *Server) handleRunsItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	runID := parts[0]
	if runID == "active" {
		s.handleActiveRun(w, r)
		return
	}
	tail := ""
	if len(parts) >= 2 {
		tail = parts[1]
	}
	switch tail {
	case "":
		// /api/runs/{id} → cancel on DELETE
		if r.Method == http.MethodDelete {
			s.handleRunCancel(w, r, runID)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	case "events":
		s.handleRunEvents(w, r, runID)
	case "state":
		s.handleRunState(w, r, runID)
	case "artifacts":
		// Convenience alias for /api/run/{id} so the SPA only has to know
		// one URL prefix.
		data, err := s.loadRun(runID)
		if err != nil {
			status := http.StatusInternalServerError
			if os.IsNotExist(err) {
				status = http.StatusNotFound
			}
			writeErr(w, status, err)
			return
		}
		writeJSON(w, data)
	case "graph":
		// GET /api/runs/{id}/graph — versioned graph export for downstream tools.
		s.handleRunGraph(w, r, runID)
	case "job":
		jobID := ""
		if len(parts) == 3 {
			jobID = parts[2]
		}
		s.handleRunJob(w, r, runID, jobID)
	case "retry":
		// POST /api/runs/{id}/retry — resume a previously failed run
		// from the failed stage onwards. Reads the failure report and
		// state files from disk, re-attaches to the retained
		// snapshot. Returns 409 if a run is already in progress.
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleRunRetry(w, r, runID)
	default:
		http.NotFound(w, r)
	}
}

// handleRun is the legacy artifact view (kept for backwards compatibility).
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/api/run/")
	runID = strings.TrimSpace(runID)
	if runID == "" || runID == "latest" {
		var err error
		runID, err = s.latestRunID()
		if err != nil {
			writeErr(w, http.StatusNotFound, err)
			return
		}
	}
	data, err := s.loadRun(runID)
	if err != nil {
		status := http.StatusInternalServerError
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		writeErr(w, status, err)
		return
	}
	writeJSON(w, data)
}

// handleRuns is the legacy listing endpoint, used by the existing JSON-only
// dashboard tests.
func (s *Server) handleRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := s.listRuns()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"runs": runs})
}

func (s *Server) listRuns() ([]string, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	runs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			runs = append(runs, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(runs)))
	return runs, nil
}

func (s *Server) latestRunID() (string, error) {
	runs, err := s.listRuns()
	if err != nil {
		return "", err
	}
	if len(runs) == 0 {
		return "", fmt.Errorf("no runs found in %s", s.baseDir)
	}
	return runs[0], nil
}

func (s *Server) loadRun(runID string) (RunData, error) {
	runDir := filepath.Join(s.baseDir, runID)
	manifestPath := filepath.Join(runDir, "run_manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return RunData{}, err
	}
	var manifest model.RunManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return RunData{}, fmt.Errorf("parse manifest: %w", err)
	}
	exposures, err := readObjectArrayDir(filepath.Join(runDir, "exposures"))
	if err != nil {
		return RunData{}, err
	}
	deps, err := readObjectArrayDir(filepath.Join(runDir, "dependencies"))
	if err != nil {
		return RunData{}, err
	}
	connections, err := readObjectArrayDir(filepath.Join(runDir, "connections"))
	if err != nil {
		return RunData{}, err
	}
	unresolved, err := readObjectArrayDir(filepath.Join(runDir, "unresolved"))
	if err != nil {
		return RunData{}, err
	}

	return RunData{
		RunID:        runID,
		Manifest:     manifest,
		Exposures:    exposures,
		Dependencies: deps,
		Connections:  connections,
		Unresolved:   unresolved,
		Counts: map[string]map[string]int{
			"exposures":    countByFile(exposures),
			"dependencies": countByFile(deps),
			"connections":  countByFile(connections),
			"unresolved":   countByFile(unresolved),
		},
	}, nil
}

func readObjectArrayDir(dir string) (map[string][]map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string][]map[string]any{}, nil
		}
		return nil, err
	}
	out := make(map[string][]map[string]any, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var items []map[string]any
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		out[name] = items
	}
	return out, nil
}

func countByFile(in map[string][]map[string]any) map[string]int {
	out := make(map[string]int, len(in))
	for k, items := range in {
		out[k] = len(items)
	}
	return out
}

// handleRunGraph serves GET /api/runs/{id}/graph — a versioned structured
// export of the run's connections in a shape optimised for downstream tools
// and the UI sequence-tree panel.
func (s *Server) handleRunGraph(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	runDir := filepath.Join(s.baseDir, runID)

	// Try a pre-built graph.v1.json first (written by the pipeline at run time).
	graphPath := filepath.Join(runDir, "state", "graph.v1.json")
	if b, err := os.ReadFile(graphPath); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}

	// Otherwise build it on demand from the run artifacts.
	data, err := s.loadRun(runID)
	if err != nil {
		status := http.StatusInternalServerError
		if os.IsNotExist(err) {
			status = http.StatusNotFound
		}
		writeErr(w, status, err)
		return
	}

	// Read infrastructure inventory if present.
	var infra map[string]any
	infraPath := filepath.Join(runDir, "state", "infrastructure.json")
	if b, err := os.ReadFile(infraPath); err == nil {
		_ = json.Unmarshal(b, &infra)
	}

	graph := buildGraphExport(runID, data, infra)
	writeJSON(w, graph)
}

// buildGraphExport assembles the graph.v1 export from run artifacts.
func buildGraphExport(runID string, data RunData, infra map[string]any) map[string]any {
	// Flatten artifacts.
	exposures := flattenObjArrayMap(data.Exposures)
	deps := flattenObjArrayMap(data.Dependencies)
	conns := flattenObjArrayMap(data.Connections)
	normalizeGraphEntities(exposures)
	normalizeGraphEntities(deps)

	// Build edges: one per connection, with steps and conditions.
	edges := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		guarantee := deriveGuarantee(c)
		edge := map[string]any{
			"from_exposure_id": c["from_exposure_id"],
			"to_dependency_id": c["to_dependency_id"],
			"path_signature":   c["path_signature"],
			"guarantee":        guarantee,
			"condition":        c["condition"],
			"paths":            c["paths"],
		}
		edges = append(edges, edge)
	}

	return map[string]any{
		"schema_version": "graph.v1",
		"run_id":         runID,
		"exposures":      exposures,
		"dependencies":   deps,
		"edges":          edges,
		"infrastructure": infra,
		"unresolved":     flattenObjArrayMap(data.Unresolved),
	}
}

func normalizeGraphEntities(items []map[string]any) {
	for _, item := range items {
		typ := stringValue(item["type"])
		details, _ := item["details"].(map[string]any)
		if details == nil {
			details = map[string]any{}
			item["details"] = details
		}
		platform := firstGraphValue(item, details, "platform", "database_type", "database", "aws_service", "cache_type")
		instance := firstGraphValue(item, details, "instance", "target_service", "service", "base_url", "target_url", "default_url", "production_url", "database_name", "database", "table_or_entity", "table", "entity", "queue", "queue_name", "destination", "topic", "stream")
		operation := firstGraphValue(item, details, "operation", "operation_normalized", "method", "http_method", "path", "endpoint")
		kind := firstGraphValue(item, details, "operation_kind")
		if platform == "" {
			platform = fallbackPlatform(typ, stringValue(item["name"]), details)
		}
		if instance == "" {
			instance = stringValue(item["name"])
		}
		if operation == "" {
			operation = stringValue(item["name"])
		}
		if kind == "" {
			kind = fallbackOperationKind(operation)
		}
		item["platform"] = platform
		item["instance"] = instance
		item["operation"] = operation
		item["operation_kind"] = kind
	}
}

func firstGraphValue(item, details map[string]any, keys ...string) string {
	for _, k := range keys {
		if v := stringValue(item[k]); v != "" {
			return v
		}
		if v := stringValue(details[k]); v != "" {
			return v
		}
	}
	return ""
}

func stringValue(v any) string {
	if v == nil {
		return ""
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "<nil>" {
		return ""
	}
	return s
}

func fallbackPlatform(typ, name string, details map[string]any) string {
	text := strings.ToLower(typ + " " + name + " " + fmt.Sprint(details))
	switch {
	case strings.Contains(text, "athena"):
		return "athena"
	case strings.Contains(text, "postgres"):
		return "postgres"
	case strings.Contains(text, "redis"):
		return "redis"
	case strings.Contains(text, "sqs"):
		return "sqs"
	case strings.Contains(text, "sns"):
		return "sns"
	case strings.Contains(text, "kafka"):
		return "kafka"
	case strings.Contains(text, "http") || strings.Contains(text, "feign") || strings.Contains(text, "api"):
		return "http"
	}
	switch typ {
	case "db_operation", "cache_operation":
		return "database"
	case "queue_consumer", "queue_publish", "stream_consume":
		return "queue"
	case "scheduled_job":
		return "scheduler"
	case "command_exec", "cli_command":
		return "process"
	}
	return typ
}

func fallbackOperationKind(operation string) string {
	fields := strings.Fields(strings.ToLower(operation))
	if len(fields) == 0 {
		return "use"
	}
	switch fields[0] {
	case "get", "post", "put", "patch", "delete", "publish", "consume", "select", "insert", "update", "save":
		return fields[0]
	}
	return "use"
}

// deriveGuarantee infers the top-level calling guarantee from a connection's paths.
func deriveGuarantee(conn map[string]any) string {
	paths, _ := conn["paths"].([]any)
	if len(paths) == 0 {
		return "unknown"
	}
	for _, pRaw := range paths {
		p, _ := pRaw.(map[string]any)
		cond, _ := p["condition"].(map[string]any)
		kind, _ := cond["kind"].(string)
		switch kind {
		case "loop", "batch":
			return "loop"
		case "if_guard", "match_arm", "ternary", "null_check":
			return "conditional"
		case "catch_block":
			return "exception_only"
		}
	}
	return "always"
}

// flattenObjArrayMap collapses a map[filename → []obj] to []obj.
func flattenObjArrayMap(in map[string][]map[string]any) []map[string]any {
	var out []map[string]any
	for _, items := range in {
		out = append(out, items...)
	}
	return out
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	writeJSON(w, map[string]any{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
