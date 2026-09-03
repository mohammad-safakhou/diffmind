// Package ui hosts the DiffMind HTTP API and the embedded web app. It exposes
// CRUD endpoints for projects, repositories, packs, and graph runs, plus
// SSE streams for live run progress, and serves the single-page UI built under
// internal/ui/web.
package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/mcpserver"
	querysvc "github.com/mohammad-safakhou/diffmind/internal/workspace/query"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/runmgr"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

// Server hosts the DiffMind dashboard API + SPA.
type Server struct {
	operationsMu        sync.Mutex
	operationsConfig    OperationsConfig
	operationsStarted   bool
	operationsStop      context.CancelFunc
	operationsWG        sync.WaitGroup
	operationsError     error
	repositoryMu        sync.Mutex
	repositoryActive    map[string]int
	repositoryTotal     int
	repositoryChanged   chan struct{}
	store               *store.Store
	query               *querysvc.Service
	runs                *runmgr.Manager
	diffmindRunsDir     string
	host                string
	port                int
	log                 *util.Logger
	version             string
	authToken           string
	proxySecret         string
	projectAccessScoped bool
	auditLogPath        string
	liveStatusMu        sync.Mutex
	liveStatusCache     map[string]liveStatusCacheEntry
	archGraphMu         sync.Mutex
	archGraphCache      map[string]archGraphCacheEntry
	refreshMu           sync.Mutex
	refreshConfig       RefreshConfig
	refreshStatus       RefreshStatus
	refreshContext      context.Context
	refreshProject      func(context.Context, string) ProjectRefreshResult
	ingestionMu         sync.Mutex
	ingestionActive     map[string]bool
	ingestionCancel     map[string]context.CancelCauseFunc
	projectOpsMu        sync.Mutex
	projectOps          map[string]bool
}

type liveStatusCacheEntry struct {
	value     repoLive
	expiresAt time.Time
}

// archGraphCacheEntry caches a parsed graph.json (tens of MB on large
// projects) keyed by the file's mtime+size; the trace UI issues many
// full-graph requests per interaction. Cached graphs are shared across
// requests and must be treated as read-only by handlers.
type archGraphCacheEntry struct {
	graph   *ArchGraph
	modTime time.Time
	size    int64
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
	server := &Server{store: st, query: querysvc.New(st), runs: runs, diffmindRunsDir: diffmindRunsDir, host: host, port: port, log: log, auditLogPath: filepath.Join(st.HomeDir(), "audit", "http.jsonl"), liveStatusCache: map[string]liveStatusCacheEntry{}, archGraphCache: map[string]archGraphCacheEntry{}, ingestionActive: map[string]bool{}, projectOps: map[string]bool{}}
	server.recoverInterruptedIngestions()
	server.ingestionCancel = map[string]context.CancelCauseFunc{}
	server.operationsConfig = OperationsConfig{Workers: 2, Capacity: 256, RepositoryWorkers: 4}
	server.repositoryActive = map[string]int{}
	server.repositoryChanged = make(chan struct{})
	return server
}

// Addr returns "host:port".
func (s *Server) Addr() string { return fmt.Sprintf("%s:%d", s.host, s.port) }

// SetVersion identifies this DiffMind build to MCP clients.
func (s *Server) SetVersion(version string) {
	if version != "" {
		s.version = version
	}
}

// Handler builds the HTTP handler (exposed for tests).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.routes(mux)
	return s.accessControlled(http.NewCrossOriginProtection().Handler(mux))
}

func (s *Server) routes(raw *http.ServeMux) {
	mux := routedMux{mux: raw, server: s}
	// Health.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// Projects (G2).
	mux.HandleFunc("GET /api/projects", s.handleListProjects)
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects/{pid}", s.handleGetProject)
	mux.HandleFunc("PATCH /api/projects/{pid}", s.requireProjectIdle(s.handlePatchProject))
	mux.HandleFunc("DELETE /api/projects/{pid}", s.requireProjectIdle(s.handleDeleteProject))

	// Repos (G3).
	mux.HandleFunc("GET /api/projects/{pid}/repos", s.handleListRepos)
	mux.HandleFunc("POST /api/projects/{pid}/repos", s.requireProjectIdle(s.handleCreateRepo))
	mux.HandleFunc("POST /api/projects/{pid}/repo-imports", s.requireProjectIdle(s.handleImportRepos))
	mux.HandleFunc("GET /api/projects/{pid}/ingestion", s.handleGetIngestion)
	mux.HandleFunc("POST /api/projects/{pid}/ingestion", s.handleStartIngestion)
	mux.HandleFunc("POST /api/projects/{pid}/ingestion/cancel", s.handleCancelIngestion)
	mux.HandleFunc("POST /api/projects/{pid}/ingestion/resume", s.handleResumeIngestion)
	mux.HandleFunc("GET /api/projects/{pid}/repos/{rid}", s.handleGetRepo)
	mux.HandleFunc("PATCH /api/projects/{pid}/repos/{rid}", s.requireProjectIdle(s.handlePatchRepo))
	mux.HandleFunc("DELETE /api/projects/{pid}/repos/{rid}", s.requireProjectIdle(s.handleDeleteRepo))
	mux.HandleFunc("POST /api/projects/{pid}/repos/{rid}/sync", s.requireProjectIdle(s.handleSyncRepo))
	mux.HandleFunc("POST /api/projects/{pid}/repos/{rid}/diffmind-runs", s.requireProjectIdle(s.handleStartDiffMindRepoRun))
	mux.HandleFunc("POST /api/projects/{pid}/diffmind-runs/batch", s.requireProjectIdle(s.handleStartDiffMindBatchRun))
	mux.HandleFunc("GET /api/projects/{pid}/repos/{rid}/diffmind-configuration-yaml", s.handleGetDiffMindConfigurationYAML)
	mux.HandleFunc("PUT /api/projects/{pid}/repos/{rid}/diffmind-configuration-yaml", s.requireProjectIdle(s.handlePutDiffMindConfigurationYAML))
	mux.HandleFunc("GET /api/projects/{pid}/repo-suggestions", s.handleRepoSuggestions)
	mux.HandleFunc("GET /api/projects/{pid}/workspace", s.handleWorkspace)
	mux.HandleFunc("GET /api/projects/{pid}/live-status", s.handleLiveStatus)
	mux.HandleFunc("GET /api/projects/{pid}/pull-requests", s.handlePullRequests)
	mux.HandleFunc("GET /api/projects/{pid}/pull-requests/{repo_id}/{number}/impact", s.handlePullRequestImpact)

	// Stable, read-only query API for integrations and company-wide clients.
	mux.HandleFunc("GET /api/v1/projects", s.handleV1Projects)
	mux.HandleFunc("GET /api/v1/jobs", s.handleJobs)
	mux.HandleFunc("POST /api/v1/projects/{pid}/refresh-jobs", s.handleEnqueueJob)
	mux.HandleFunc("POST /api/v1/jobs/{jid}/cancel", s.handleCancelJob)
	mux.HandleFunc("POST /api/v1/jobs/{jid}/retry", s.handleRetryJob)
	mux.HandleFunc("GET /api/v1/projects/{pid}/ingestion-history", s.handleIngestionHistory)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("POST /api/v1/projects/{pid}/webhooks/github", s.handleGitHubWebhook)
	mux.HandleFunc("GET /api/v1/session", s.handleSession)
	mux.HandleFunc("GET /api/v1/projects/{pid}/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /api/v1/projects/{pid}/access", s.handleGetAccess)
	mux.HandleFunc("PUT /api/v1/projects/{pid}/access", s.handlePutAccess)
	mux.HandleFunc("GET /api/v1/projects/{pid}/limits", s.handleGetLimits)
	mux.HandleFunc("PUT /api/v1/projects/{pid}/limits", s.handlePutLimits)
	mux.HandleFunc("GET /api/v1/projects/{pid}/tokens", s.handleListTokens)
	mux.HandleFunc("POST /api/v1/projects/{pid}/tokens", s.handleIssueToken)
	mux.HandleFunc("POST /api/v1/projects/{pid}/tokens/{tid}/revoke", s.handleRevokeToken)
	mux.HandleFunc("GET /api/v1/projects/{pid}/graph/summary", s.handleV1GraphSummary)
	mux.HandleFunc("GET /api/v1/projects/{pid}/graph/runs", s.handleV1GraphRuns)
	mux.HandleFunc("GET /api/v1/projects/{pid}/graph/compare", s.handleV1GraphCompare)
	mux.HandleFunc("GET /api/v1/projects/{pid}/graph/path", s.handleV1GraphPath)
	mux.HandleFunc("GET /api/v1/projects/{pid}/graph/trace", s.handleV1ObjectTrace)
	mux.HandleFunc("GET /api/v1/projects/{pid}/services", s.handleV1Services)
	mux.HandleFunc("GET /api/v1/projects/{pid}/services/{service}", s.handleV1Service)
	mux.HandleFunc("GET /api/v1/projects/{pid}/dependencies", s.handleV1Dependencies)
	mux.HandleFunc("GET /api/v1/projects/{pid}/impact", s.handleV1Impact)
	mux.HandleFunc("GET /api/v1/projects/{pid}/search", s.handleV1Search)
	mux.HandleFunc("GET /api/v1/refresh/status", s.handleRefreshStatus)
	mux.HandleFunc("POST /api/v1/refresh", s.handleRefreshNow)

	// Remote Model Context Protocol endpoint for company-wide coding agents.
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return mcpserver.New(s.queryFor(r), "", s.version).MCPServer()
	}, &mcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute, Stateless: s.projectAccessScoped, JSONResponse: s.projectAccessScoped}))

	// Packs (G4).
	mux.HandleFunc("GET /api/projects/{pid}/packs", s.handleListPacks)
	mux.HandleFunc("POST /api/projects/{pid}/packs", s.requireProjectIdle(s.handleCreatePack))
	mux.HandleFunc("GET /api/projects/{pid}/packs/{pack_id}", s.handleGetPack)
	mux.HandleFunc("PUT /api/projects/{pid}/packs/{pack_id}", s.requireProjectIdle(s.handlePutPack))
	mux.HandleFunc("DELETE /api/projects/{pid}/packs/{pack_id}", s.requireProjectIdle(s.handleDeletePack))

	// DiffMind run discovery (G5).
	mux.HandleFunc("GET /api/diffmind-runs", s.handleDiffMindRuns)

	// Graph runs (G6).
	mux.HandleFunc("GET /api/projects/{pid}/runs", s.handleListRuns)
	mux.HandleFunc("POST /api/projects/{pid}/runs", s.requireProjectIdle(s.handleCreateRun))
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}", s.handleGetRun)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/events", s.handleRunEvents)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/graph", s.handleRunGraph)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph", s.handleRunArchGraph)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/teams/{team}", s.handleRunArchGraphTeam)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/services/{service}", s.handleRunArchGraphService)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/resources/{resource}", s.handleRunArchGraphResource)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/trace", s.handleRunArchGraphTrace)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/flow", s.handleRunArchGraphFlow)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/entrypoints", s.handleRunArchGraphEntrypoints)
	mux.HandleFunc("GET /api/projects/{pid}/runs/{rid}/archgraph/impact", s.handleRunArchGraphImpact)
	mux.HandleFunc("POST /api/projects/{pid}/runs/{rid}/cancel", s.handleCancelRun)
	mux.HandleFunc("DELETE /api/projects/{pid}/runs/{rid}", s.requireProjectIdle(s.handleDeleteRun))

	// SPA (catch-all).
	mux.HandleFunc("/", s.handleStatic)
}

// Start runs the HTTP server until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	if err := s.StartOperations(ctx); err != nil {
		return err
	}
	defer s.StopOperations()
	s.refreshMu.Lock()
	s.refreshContext = ctx
	s.refreshMu.Unlock()
	s.resumeInterruptedIngestions(ctx)
	s.startRefreshLoop(ctx)
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
