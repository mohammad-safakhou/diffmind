// Package ui hosts the DiffMind HTTP API and the embedded web app. It exposes
// CRUD endpoints for projects, repositories, blueprints, and graph runs, plus
// SSE streams for live run progress, and serves the single-page UI built under
// internal/ui/web.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/store"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Server hosts the DiffMind dashboard API + SPA.
type Server struct {
	store           *store.Store
	runs            *runmgr.Manager
	diffmindRunsDir string
	host            string
	port            int
	log             *util.Logger
	liveStatusMu    sync.Mutex
	liveStatusCache map[string]liveStatusCacheEntry
}

type liveStatusCacheEntry struct {
	value     repoLive
	expiresAt time.Time
}

// New constructs a Server backed by the given store and run manager.
func New(st *store.Store, runs *runmgr.Manager, diffmindRunsDir, host string, port int, log *util.Logger) *Server {
	if host == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 8090
	}
	if log == nil {
		log = util.NewLogger(util.LevelInfo)
	}
	return &Server{store: st, runs: runs, diffmindRunsDir: diffmindRunsDir, host: host, port: port, log: log, liveStatusCache: map[string]liveStatusCacheEntry{}}
}

// Addr returns "host:port".
func (s *Server) Addr() string { return fmt.Sprintf("%s:%d", s.host, s.port) }

// Handler builds the HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return mux
}

func (s *Server) routes(mux *http.ServeMux) {
	// Health.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// Projects (G2).
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{pid}", s.handleGetProject)
	mux.HandleFunc("PATCH /api/projects/{pid}", s.handlePatchProject)
	mux.HandleFunc("DELETE /api/projects/{pid}", s.handleDeleteProject)

	// Repos (G3).
	mux.HandleFunc("GET /api/projects/{pid}/repos", s.handleListRepos)
	mux.HandleFunc("POST /api/projects/{pid}/repos", s.handleCreateRepo)
	mux.HandleFunc("POST /api/projects/{pid}/repo-imports", s.handleImportRepos)
	mux.HandleFunc("GET /api/projects/{pid}/repos/{rid}", s.handleGetRepo)
	mux.HandleFunc("PATCH /api/projects/{pid}/repos/{rid}", s.handlePatchRepo)
	mux.HandleFunc("DELETE /api/projects/{pid}/repos/{rid}", s.handleDeleteRepo)
	mux.HandleFunc("POST /api/projects/{pid}/repos/{rid}/sync", s.handleSyncRepo)
	mux.HandleFunc("POST /api/projects/{pid}/repos/{rid}/diffmind-runs", s.handleStartDiffMindRepoRun)
	mux.HandleFunc("POST /api/projects/{pid}/diffmind-runs/batch", s.handleStartDiffMindBatchRun)
	mux.HandleFunc("GET /api/projects/{pid}/repos/{rid}/diffmind-configuration-yaml", s.handleGetDiffMindConfigurationYAML)
	mux.HandleFunc("PUT /api/projects/{pid}/repos/{rid}/diffmind-configuration-yaml", s.handlePutDiffMindConfigurationYAML)
	mux.HandleFunc("GET /api/projects/{pid}/repo-suggestions", s.handleRepoSuggestions)
	mux.HandleFunc("GET /api/projects/{pid}/workspace", s.handleWorkspace)
	mux.HandleFunc("GET /api/projects/{pid}/live-status", s.handleLiveStatus)

	// Blueprints (G4).
	mux.HandleFunc("GET /api/projects/{pid}/blueprints", s.handleListBlueprints)
	mux.HandleFunc("POST /api/projects/{pid}/blueprints", s.handleCreateBlueprint)
	mux.HandleFunc("GET /api/projects/{pid}/blueprints/{bid}", s.handleGetBlueprint)
	mux.HandleFunc("PUT /api/projects/{pid}/blueprints/{bid}", s.handlePutBlueprint)
	mux.HandleFunc("DELETE /api/projects/{pid}/blueprints/{bid}", s.handleDeleteBlueprint)

	// DiffMind run discovery (G5).
	mux.HandleFunc("GET /api/diffmind-runs", s.handleDiffMindRuns)

	// Graph runs (G6).
	mux.HandleFunc("GET /api/projects/{pid}/runs", s.handleListRuns)
	mux.HandleFunc("POST /api/projects/{pid}/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}", s.handleGetRun)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/events", s.handleRunEvents)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/graph", s.handleRunGraph)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph", s.handleRunArchGraph)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/teams/{team}", s.handleRunArchGraphTeam)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/services/{service}", s.handleRunArchGraphService)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/resources/{resource}", s.handleRunArchGraphResource)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/trace", s.handleRunArchGraphTrace)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/flow", s.handleRunArchGraphFlow)
	mux.HandleFunc("POST /api/projects/{pid}/runs/{rid}/cancel", s.handleCancelRun)
	mux.HandleFunc("DELETE /api/projects/{pid}/runs/{rid}", s.handleDeleteRun)

	// SPA (catch-all).
	mux.HandleFunc("/", s.handleStatic)
}

// Start runs the HTTP server until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{Addr: s.Addr(), Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		s.log.Info("diffmind dashboard listening", "addr", s.Addr())
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

// ---- shared JSON helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]any{"error": err.Error()})
}

func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func marshalBody(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
