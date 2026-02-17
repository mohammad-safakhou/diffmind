package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"diffmind/internal/audit"
	"diffmind/internal/bundleio"
	"diffmind/internal/diff"
	graphpkg "diffmind/internal/graph"
	"diffmind/internal/graphschema"
	"diffmind/internal/query"
	"diffmind/internal/security"
)

//go:embed ui/*
var uiFiles embed.FS

type options struct {
	Addr       string
	BundlePath string
	GraphRoot  string
}

type uiDefaults struct {
	DefaultBundlePath string `json:"default_bundle_path"`
	GraphRoot         string `json:"graph_root"`
	BuildDefaults     struct {
		Mode               string `json:"mode"`
		OutDir             string `json:"out_dir"`
		ManifestPath       string `json:"manifest_path"`
		ServiceID          string `json:"service_id"`
		ServiceName        string `json:"service_name"`
		BundlePath         string `json:"bundle_path"`
		AnalyzerBundlePath string `json:"analyzer_bundle_path"`
	} `json:"build_defaults"`
}

type graphFilters struct {
	IncludeInferred bool
	EdgeTypeFilter  string
	ServiceFilter   string
	RepoFilter      string
	NodeFilter      string
	ConfidenceMin   float64
	Verification    string
	ConflictStatus  string
	Environment     string
	QueryText       string
}

type metricItem struct {
	NodeID string `json:"node_id"`
	Label  string `json:"label"`
	Count  int    `json:"count"`
}

type graphMetrics struct {
	GraphID         string         `json:"graph_id"`
	NodeCount       int            `json:"node_count"`
	EdgeCount       int            `json:"edge_count"`
	EdgeTypeCounts  map[string]int `json:"edge_type_counts"`
	TopCallers      []metricItem   `json:"top_callers"`
	TopDependencies []metricItem   `json:"top_dependencies"`
}

type evidenceNode struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	ServiceID string `json:"service_id,omitempty"`
}

type evidenceProvenance struct {
	Graph         graphschema.GraphProvenance   `json:"graph"`
	SourceService graphschema.ServiceProvenance `json:"source_service,omitempty"`
	TargetService graphschema.ServiceProvenance `json:"target_service,omitempty"`
}

type evidenceDetail struct {
	GraphID       string                    `json:"graph_id"`
	EdgeID        string                    `json:"edge_id"`
	EdgeType      string                    `json:"edge_type"`
	Confidence    float64                   `json:"confidence"`
	Inferred      bool                      `json:"inferred"`
	Source        evidenceNode              `json:"source"`
	Target        evidenceNode              `json:"target"`
	SourceService *graphschema.ServiceMeta  `json:"source_service,omitempty"`
	TargetService *graphschema.ServiceMeta  `json:"target_service,omitempty"`
	EvidenceRefs  []graphschema.EvidenceRef `json:"evidence_refs"`
	Provenance    evidenceProvenance        `json:"provenance"`
}

type graphCompareRequest struct {
	FromGraphID string `json:"from_graph_id"`
	ToGraphID   string `json:"to_graph_id"`
}

type nodeCompareItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	ServiceID string `json:"service_id,omitempty"`
}

type edgeCompareItem struct {
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	SourceID   string  `json:"source_id"`
	TargetID   string  `json:"target_id"`
	Confidence float64 `json:"confidence"`
}

type nodeChangeItem struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	ServiceID string         `json:"service_id,omitempty"`
	Keys      []string       `json:"keys"`
	Before    map[string]any `json:"before,omitempty"`
	After     map[string]any `json:"after,omitempty"`
}

type edgeChangeItem struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	SourceID string         `json:"source_id"`
	TargetID string         `json:"target_id"`
	Keys     []string       `json:"keys"`
	Before   map[string]any `json:"before,omitempty"`
	After    map[string]any `json:"after,omitempty"`
}

type graphCompare struct {
	CompareID    string            `json:"compare_id,omitempty"`
	GeneratedAt  time.Time         `json:"generated_at,omitempty"`
	TenantID     string            `json:"tenant_id,omitempty"`
	FromGraphID  string            `json:"from_graph_id"`
	ToGraphID    string            `json:"to_graph_id"`
	FromCounts   map[string]int    `json:"from_counts"`
	ToCounts     map[string]int    `json:"to_counts"`
	AddedNodes   []nodeCompareItem `json:"added_nodes"`
	RemovedNodes []nodeCompareItem `json:"removed_nodes"`
	ChangedNodes []nodeChangeItem  `json:"changed_nodes"`
	AddedEdges   []edgeCompareItem `json:"added_edges"`
	RemovedEdges []edgeCompareItem `json:"removed_edges"`
	ChangedEdges []edgeChangeItem  `json:"changed_edges"`
}

type compareSummary struct {
	CompareID    string    `json:"compare_id"`
	GeneratedAt  time.Time `json:"generated_at"`
	TenantID     string    `json:"tenant_id,omitempty"`
	FromGraphID  string    `json:"from_graph_id"`
	ToGraphID    string    `json:"to_graph_id"`
	AddedNodes   int       `json:"added_nodes"`
	RemovedNodes int       `json:"removed_nodes"`
	ChangedNodes int       `json:"changed_nodes"`
	AddedEdges   int       `json:"added_edges"`
	RemovedEdges int       `json:"removed_edges"`
	ChangedEdges int       `json:"changed_edges"`
	Path         string    `json:"path"`
}

type compareIndex struct {
	Compares   []compareSummary `json:"compares"`
	NextBefore string           `json:"next_before,omitempty"`
}

type routeMetrics struct {
	Route            string    `json:"route"`
	Requests         int64     `json:"requests"`
	Errors           int64     `json:"errors"`
	AuthFailures     int64     `json:"auth_failures"`
	TotalDurationMS  float64   `json:"total_duration_ms"`
	P95DurationMS    float64   `json:"p95_duration_ms"`
	LastStatus       int       `json:"last_status"`
	LastAccessedAt   time.Time `json:"last_accessed_at,omitempty"`
	AvailabilityRate float64   `json:"availability_rate"`
}

type runtimeMetrics struct {
	mu     sync.Mutex
	routes map[string]*routeMetrics
}

var apiMetrics = &runtimeMetrics{routes: map[string]*routeMetrics{}}

type productPRReviewRequest struct {
	GraphID      string   `json:"graph_id"`
	ChangedNodes []string `json:"changed_nodes"`
	MaxFindings  int      `json:"max_findings"`
}

type productFinding struct {
	Severity string         `json:"severity"`
	Title    string         `json:"title"`
	NodeID   string         `json:"node_id,omitempty"`
	EdgeID   string         `json:"edge_id,omitempty"`
	Details  map[string]any `json:"details,omitempty"`
}

type productPRReviewResponse struct {
	GraphID   string           `json:"graph_id"`
	Findings  []productFinding `json:"findings"`
	Summary   map[string]int   `json:"summary"`
	Generated time.Time        `json:"generated_at"`
}

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              opts.Addr,
		Handler:           newMux(opts.BundlePath, opts.GraphRoot),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve http api: %w", err)
	}
	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "HTTP listen address")
	bundle := fs.String("bundle", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Default canonical intelligence bundle path")
	graphRoot := fs.String("graph-root", filepath.Join(".diffmind", "graph"), "Graph artifacts root directory")

	if err := fs.Parse(filterArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse serve flags: %w", err)
	}
	return options{
		Addr:       strings.TrimSpace(*addr),
		BundlePath: strings.TrimSpace(*bundle),
		GraphRoot:  strings.TrimSpace(*graphRoot),
	}, nil
}

func filterArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--addr" || arg == "--bundle" || arg == "--graph-root":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case strings.HasPrefix(arg, "--addr=") || strings.HasPrefix(arg, "--bundle=") || strings.HasPrefix(arg, "--graph-root="):
			out = append(out, arg)
		}
	}
	return out
}

func newMux(defaultBundlePath string, graphRoot string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/health", instrument("/health", http.HandlerFunc(handleHealth)))
	mux.Handle("/defaults", instrument("/defaults", handleDefaults(defaultBundlePath, graphRoot)))
	mux.Handle("/entities", instrument("/entities", handleEntities(defaultBundlePath)))
	mux.Handle("/diff", instrument("/diff", http.HandlerFunc(handleDiff)))
	mux.Handle("/graphs", instrument("/graphs", handleGraphs(graphRoot)))
	mux.Handle("/graphs/at", instrument("/graphs/at", handleGraphAt(graphRoot)))
	mux.Handle("/graphs/build", instrument("/graphs/build", handleGraphBuildAlias(graphRoot)))
	mux.Handle("/graphs/compare", instrument("/graphs/compare", handleGraphsCompare(graphRoot)))
	mux.Handle("/graphs/compare/", instrument("/graphs/compare/:id", handleGraphsCompareByID(graphRoot)))
	mux.Handle("/graphs/", instrument("/graphs/:id", handleGraphByID(graphRoot)))
	mux.Handle("/compliance/audit", instrument("/compliance/audit", handleAuditList(graphRoot)))
	mux.Handle("/compliance/audit/export", instrument("/compliance/audit/export", handleAuditExport(graphRoot)))
	mux.Handle("/compliance/audit/retention", instrument("/compliance/audit/retention", handleAuditRetention(graphRoot)))
	mux.Handle("/products/pr-review", instrument("/products/pr-review", handleProductPRReview(graphRoot)))
	mux.Handle("/products/docs/", instrument("/products/docs/:graph_id", handleProductDocs(graphRoot)))
	mux.Handle("/products/mapper/", instrument("/products/mapper/:graph_id", handleProductMapper(graphRoot)))
	mux.Handle("/products/governance/", instrument("/products/governance/:graph_id", handleProductGovernance(graphRoot)))
	mux.Handle("/ops/metrics", instrument("/ops/metrics", handleOpsMetrics(graphRoot)))
	mux.Handle("/ops/slo", instrument("/ops/slo", handleOpsSLO(graphRoot)))
	if sub, err := fs.Sub(uiFiles, "ui"); err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	}
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func instrument(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, req)
		apiMetrics.record(route, rec.status, time.Since(start))
	})
}

func handleGraphBuildAlias(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		handleGraphsBuild(graphRoot, w, r)
	}
}

func handleGraphsCompare(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionCompareGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			listCompareHistory(graphRoot, r, w)
			return
		case http.MethodDelete:
			authCtx, ok := authorizeRequest(w, r, security.ActionDeleteCompare, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			keepLatest := 0
			if q := strings.TrimSpace(r.URL.Query().Get("keep_latest")); q != "" {
				n, err := strconv.Atoi(q)
				if err != nil || n < 0 {
					writeError(w, http.StatusBadRequest, "invalid keep_latest")
					return
				}
				keepLatest = n
			}
			deleted, err := pruneCompareHistory(graphRoot, keepLatest)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionDeleteCompare, "allow", "compare_history_pruned", map[string]any{"deleted": deleted})
			writeJSON(w, http.StatusOK, map[string]any{
				"deleted":       deleted,
				"keep_latest":   keepLatest,
				"history_prune": true,
			})
			return
		case http.MethodPost:
			// continue below
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionCompareGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot)
		if !ok {
			return
		}
		var req graphCompareRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.FromGraphID = strings.TrimSpace(req.FromGraphID)
		req.ToGraphID = strings.TrimSpace(req.ToGraphID)
		if req.FromGraphID == "" || req.ToGraphID == "" {
			writeError(w, http.StatusBadRequest, "from_graph_id and to_graph_id are required")
			return
		}

		fromGraph, err := loadGraph(graphRoot, req.FromGraphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "from graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		toGraph, err := loadGraph(graphRoot, req.ToGraphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "to graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		if graphTenant(fromGraph) != graphTenant(toGraph) {
			writeError(w, http.StatusBadRequest, "cross-tenant compare is not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionCompareGraph, graphTenant(fromGraph), false, false, graphRoot); !ok {
			return
		}
		filters, err := parseGraphFilters(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid confidence_min")
			return
		}
		if hasGraphFilter(filters) {
			fromGraph = filterGraph(fromGraph, filters)
			toGraph = filterGraph(toGraph, filters)
		}
		result := buildGraphCompare(fromGraph, toGraph)
		result.TenantID = graphTenant(fromGraph)
		if err := persistCompareResult(graphRoot, &result); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionCompareGraph, "allow", "compare_created", map[string]any{"compare_id": result.CompareID})
		writeJSON(w, http.StatusOK, result)
	}
}

func handleGraphsCompareByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		compareID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/graphs/compare/"))
		compareID = strings.Trim(compareID, "/")
		if compareID == "" {
			writeError(w, http.StatusBadRequest, "compare id is required")
			return
		}

		switch r.Method {
		case http.MethodGet:
			payload, err := loadCompareResult(graphRoot, compareID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, ok := authorizeRequest(w, r, security.ActionCompareGraph, normalizeTenant(payload.TenantID), false, false, graphRoot); !ok {
				return
			}
			writeJSON(w, http.StatusOK, payload)
		case http.MethodDelete:
			payload, err := loadCompareResult(graphRoot, compareID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			authCtx, ok := authorizeRequest(w, r, security.ActionDeleteCompare, normalizeTenant(payload.TenantID), false, true, graphRoot)
			if !ok {
				return
			}
			if err := deleteCompareResult(graphRoot, compareID); err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recordAudit(graphRoot, authCtx, r, security.ActionDeleteCompare, "allow", "compare_deleted", map[string]any{"compare_id": compareID})
			writeJSON(w, http.StatusOK, map[string]any{
				"deleted":    true,
				"compare_id": compareID,
			})
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
	})
}

func handleEntities(defaultBundlePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryEntities, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, filepath.Join(".diffmind", "graph"))
		if !ok {
			return
		}
		view := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("view")))
		if view == "" {
			view = "all"
		}
		if !query.ValidateView(view) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported view %q", view))
			return
		}

		bundlePath := strings.TrimSpace(r.URL.Query().Get("bundle"))
		if bundlePath == "" {
			bundlePath = defaultBundlePath
		}

		b, err := bundleio.Load(bundlePath)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		rows := query.FilterEntities(b.Entities, view)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Type == rows[j].Type {
				return rows[i].NaturalKey < rows[j].NaturalKey
			}
			return rows[i].Type < rows[j].Type
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"snapshot_id": b.SnapshotID,
			"view":        view,
			"count":       len(rows),
			"entities":    rows,
		})
		_ = authCtx
	}
}

func handleDiff(w http.ResponseWriter, r *http.Request) {
	if _, ok := authorizeRequest(w, r, security.ActionQueryEntities, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, filepath.Join(".diffmind", "graph")); !ok {
		return
	}
	fromPath := strings.TrimSpace(r.URL.Query().Get("from"))
	toPath := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromPath == "" || toPath == "" {
		writeError(w, http.StatusBadRequest, "missing required query params: from and to")
		return
	}

	fromBundle, err := bundleio.Load(fromPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	toBundle, err := bundleio.Load(toPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, diff.BuildReport(fromBundle, toBundle))
}

func handleGraphs(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGraphsList(graphRoot, w, r)
			return
		case http.MethodPost:
			handleGraphsBuild(graphRoot, w, r)
			return
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleGraphsList(graphRoot string, w http.ResponseWriter, r *http.Request) {
	authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot)
	if !ok {
		return
	}
	indexPath := filepath.Join(graphRoot, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, graphschema.Index{Graphs: []graphschema.Summary{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var index graphschema.Index
	if err := json.Unmarshal(data, &index); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode graph index: %v", err))
		return
	}
	if !authCtx.HasRole("platform_admin") {
		filtered := make([]graphschema.Summary, 0, len(index.Graphs))
		for _, g := range index.Graphs {
			if summaryTenant(g) == normalizeTenant(authCtx.TenantID) {
				filtered = append(filtered, g)
			}
		}
		index.Graphs = filtered
	}
	writeJSON(w, http.StatusOK, index)
}

type graphBuildRequest struct {
	ManifestPath       string   `json:"manifest_path"`
	Sources            []string `json:"sources"`
	OutDir             string   `json:"out_dir"`
	Persist            bool     `json:"persist"`
	Mode               string   `json:"mode"`
	ServiceID          string   `json:"service_id"`
	ServiceName        string   `json:"service_name"`
	BundlePath         string   `json:"bundle_path"`
	AnalyzerBundlePath string   `json:"analyzer_bundle_path"`
	BaseURLs           []string `json:"base_urls"`
}

func handleGraphsBuild(graphRoot string, w http.ResponseWriter, r *http.Request) {
	authCtx, ok := authorizeRequest(w, r, security.ActionBuildGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %v", err))
		return
	}

	var req graphBuildRequest
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
	}
	if req.OutDir == "" {
		req.OutDir = defaultOutDirFromGraphRoot(graphRoot)
	}
	if req.BundlePath == "" {
		req.BundlePath = filepath.Join(req.OutDir, "bundle", "intelligence_bundle.json")
	}
	if req.AnalyzerBundlePath == "" {
		req.AnalyzerBundlePath = filepath.Join(req.OutDir, "analyzers", "bundle.json")
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode == "" {
		req.Mode = "auto"
	}
	// Multi mode fallback: if client does not provide sources and manifest is absent,
	// auto-discover from out_dir and its parent so UI works with minimal input.
	if req.Mode == "multi" && len(req.Sources) == 0 {
		manifestPath := strings.TrimSpace(req.ManifestPath)
		if manifestPath == "" {
			manifestPath = filepath.Join(req.OutDir, "graph", "services.yaml")
		}
		if !fileExists(manifestPath) {
			req.Sources = discoverSourcesFromOutDir(req.OutDir)
			req.ManifestPath = ""
		}
	}

	result, err := graphpkg.Build(r.Context(), graphpkg.BuildRequest{
		ManifestPath:       req.ManifestPath,
		Sources:            req.Sources,
		OutDir:             req.OutDir,
		Persist:            req.Persist,
		TenantID:           normalizeTenant(authCtx.TenantID),
		Mode:               req.Mode,
		ServiceID:          req.ServiceID,
		ServiceName:        req.ServiceName,
		BundlePath:         req.BundlePath,
		AnalyzerBundlePath: req.AnalyzerBundlePath,
		BaseURLs:           req.BaseURLs,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "deny", "build_failed", map[string]any{"error": err.Error()})
		return
	}
	recordAudit(graphRoot, authCtx, r, security.ActionBuildGraph, "allow", "build_succeeded", map[string]any{"graph_id": result.GraphID})
	writeJSON(w, http.StatusCreated, result)
}

func handleDefaults(defaultBundlePath string, graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		outDir := defaultOutDirFromGraphRoot(graphRoot)
		resp := uiDefaults{
			DefaultBundlePath: defaultBundlePath,
			GraphRoot:         graphRoot,
		}
		resp.BuildDefaults.Mode = "auto"
		resp.BuildDefaults.OutDir = outDir
		resp.BuildDefaults.ManifestPath = filepath.Join(outDir, "graph", "services.yaml")
		resp.BuildDefaults.ServiceID = "service.local"
		resp.BuildDefaults.ServiceName = "Local Service"
		resp.BuildDefaults.BundlePath = filepath.Join(outDir, "bundle", "intelligence_bundle.json")
		resp.BuildDefaults.AnalyzerBundlePath = filepath.Join(outDir, "analyzers", "bundle.json")
		writeJSON(w, http.StatusOK, resp)
	}
}

func discoverSourcesFromOutDir(outDir string) []string {
	out := make([]string, 0, 2)
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return
		}
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			return
		}
		if _, exists := seen[abs]; exists {
			return
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	add(outDir)
	parent := filepath.Dir(strings.TrimSpace(outDir))
	if parent != "" && parent != "." && parent != outDir {
		add(parent)
	}
	return out
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func defaultOutDirFromGraphRoot(graphRoot string) string {
	trimmed := strings.TrimSpace(graphRoot)
	if trimmed == "" {
		return ".diffmind"
	}
	cleaned := filepath.Clean(trimmed)
	if filepath.Base(cleaned) == "graph" {
		return filepath.Dir(cleaned)
	}
	return cleaned
}

func handleGraphByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/graphs/")
		rest = strings.TrimSpace(strings.Trim(rest, "/"))
		if rest == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		parts := strings.Split(rest, "/")
		graphID := parts[0]
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}

		if len(parts) == 3 && parts[1] == "evidence" {
			authCtx, ok = authorizeRequest(w, r, security.ActionReadEvidence, graphTenant(graph), true, false, graphRoot)
			if !ok {
				return
			}
			edgeID := strings.TrimSpace(parts[2])
			if edgeID == "" {
				writeError(w, http.StatusBadRequest, "edge id is required")
				return
			}
			nodeByID := map[string]graphschema.Node{}
			for _, n := range graph.Nodes {
				nodeByID[n.ID] = n
			}
			serviceByID := map[string]graphschema.ServiceMeta{}
			for _, svc := range graph.Meta.Services {
				serviceByID[svc.ID] = svc
			}

			for _, e := range graph.Edges {
				if e.ID == edgeID {
					srcNode := nodeByID[e.SourceID]
					dstNode := nodeByID[e.TargetID]
					srcService, srcOK := serviceByID[srcNode.ServiceID]
					dstService, dstOK := serviceByID[dstNode.ServiceID]

					payload := evidenceDetail{
						GraphID:    graphID,
						EdgeID:     edgeID,
						EdgeType:   e.Type,
						Confidence: e.Confidence,
						Inferred:   e.Inferred,
						Source: evidenceNode{
							ID:        srcNode.ID,
							Type:      srcNode.Type,
							Label:     srcNode.Label,
							ServiceID: srcNode.ServiceID,
						},
						Target: evidenceNode{
							ID:        dstNode.ID,
							Type:      dstNode.Type,
							Label:     dstNode.Label,
							ServiceID: dstNode.ServiceID,
						},
						EvidenceRefs: e.EvidenceRefs,
						Provenance: evidenceProvenance{
							Graph: graph.Meta.Provenance,
						},
					}
					if srcOK {
						svc := srcService
						payload.SourceService = &svc
						payload.Provenance.SourceService = svc.Provenance
					}
					if dstOK {
						svc := dstService
						payload.TargetService = &svc
						payload.Provenance.TargetService = svc.Provenance
					}

					includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
					_ = includeSensitive
					if !security.CanReadRawEvidence(authCtx) {
						for i := range payload.EvidenceRefs {
							payload.EvidenceRefs[i].FilePath = "[REDACTED]"
							payload.EvidenceRefs[i].StartLine = 0
							payload.EvidenceRefs[i].StartCol = 0
							payload.EvidenceRefs[i].EndLine = 0
							payload.EvidenceRefs[i].EndCol = 0
						}
					}
					writeJSON(w, http.StatusOK, payload)
					return
				}
			}
			writeError(w, http.StatusNotFound, "edge not found")
			return
		}

		filters, err := parseGraphFilters(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid confidence_min")
			return
		}

		if hasGraphFilter(filters) {
			graph = filterGraph(graph, filters)
		}
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)
		graph = annotateGraphFreshness(graph, time.Now().UTC(), parseMaxAgeHours(r.URL.Query().Get("max_age_hours")))
		if len(parts) == 2 && parts[1] == "query" {
			explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
			if !explain {
				writeJSON(w, http.StatusOK, graph)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"graph":   graph,
				"explain": buildGraphExplain(graph),
			})
			return
		}
		if len(parts) == 2 && parts[1] == "metrics" {
			writeJSON(w, http.StatusOK, buildGraphMetrics(graph))
			return
		}
		writeJSON(w, http.StatusOK, graph)
	}
}

func handleGraphAt(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		at, err := parseTimeTravelAt(r.URL.Query().Get("at"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))

		index, err := loadGraphIndex(graphRoot)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph index not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summary, ok := selectGraphAt(index, at, mode)
		if !ok {
			writeError(w, http.StatusNotFound, "no graph available for requested time")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, summaryTenant(summary), false, false, graphRoot)
		if !ok {
			return
		}

		graph, err := loadGraphByPath(summary.Path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		filters, err := parseGraphFilters(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid confidence_min")
			return
		}
		if hasGraphFilter(filters) {
			graph = filterGraph(graph, filters)
		}
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)
		graph = annotateGraphFreshness(graph, time.Now().UTC(), parseMaxAgeHours(r.URL.Query().Get("max_age_hours")))
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, graph)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"graph":   graph,
			"explain": buildGraphExplain(graph),
		})
	}
}

func authorizeRequest(w http.ResponseWriter, r *http.Request, action security.Action, resourceTenant string, sensitive bool, mutating bool, graphRoot string) (security.Context, bool) {
	ctx, err := security.ContextFromHeaders(r.Header)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		recordAudit(graphRoot, security.Context{}, r, action, "deny", "missing_auth_context", map[string]any{
			"error": err.Error(),
		})
		return security.Context{}, false
	}
	decision := security.Authorize(ctx, security.Request{
		Action:         action,
		ResourceTenant: normalizeTenant(resourceTenant),
		Method:         r.Method,
		Path:           r.URL.Path,
		Sensitive:      sensitive,
		Mutating:       mutating,
	})
	if !decision.Allow {
		writeError(w, http.StatusForbidden, decision.Reason)
		recordAudit(graphRoot, ctx, r, action, "deny", decision.Reason, nil)
		return security.Context{}, false
	}
	recordAudit(graphRoot, ctx, r, action, "allow", decision.Reason, nil)
	return ctx, true
}

func normalizeTenant(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}

func graphTenant(graph graphschema.Graph) string {
	return normalizeTenant(graph.Meta.TenantID)
}

func summaryTenant(s graphschema.Summary) string {
	return normalizeTenant(s.TenantID)
}

func recordAudit(graphRoot string, ctx security.Context, r *http.Request, action security.Action, decision string, reason string, metadata map[string]any) {
	_ = audit.AppendEvent(filepath.Dir(graphRoot), audit.Event{
		Timestamp: time.Now().UTC(),
		Action:    string(action),
		TenantID:  normalizeTenant(ctx.TenantID),
		Principal: strings.TrimSpace(ctx.Principal),
		Method:    r.Method,
		Path:      r.URL.Path,
		Decision:  decision,
		Reason:    reason,
		Metadata:  metadata,
	})
}

func loadGraph(graphRoot string, graphID string) (graphschema.Graph, error) {
	graphPath := filepath.Join(graphRoot, graphID, "graph.json")
	return loadGraphByPath(graphPath)
}

func loadGraphByPath(graphPath string) (graphschema.Graph, error) {
	data, err := os.ReadFile(graphPath)
	if err != nil {
		return graphschema.Graph{}, err
	}
	var graph graphschema.Graph
	if err := json.Unmarshal(data, &graph); err != nil {
		return graphschema.Graph{}, fmt.Errorf("decode graph: %w", err)
	}
	return graph, nil
}

func loadGraphIndex(graphRoot string) (graphschema.Index, error) {
	indexPath := filepath.Join(graphRoot, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return graphschema.Index{}, err
	}
	var index graphschema.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return graphschema.Index{}, fmt.Errorf("decode graph index: %w", err)
	}
	return index, nil
}

func parseTimeTravelAt(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
		ts := time.Unix(unix, 0).UTC()
		return &ts, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("invalid at timestamp: expected RFC3339 or unix seconds")
	}
	u := ts.UTC()
	return &u, nil
}

func selectGraphAt(index graphschema.Index, at *time.Time, mode string) (graphschema.Summary, bool) {
	candidates := make([]graphschema.Summary, 0, len(index.Graphs))
	for _, s := range index.Graphs {
		if mode != "" && strings.ToLower(strings.TrimSpace(s.Mode)) != mode {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		return graphschema.Summary{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].GeneratedAt.After(candidates[j].GeneratedAt)
	})
	if at == nil {
		return candidates[0], true
	}
	for _, s := range candidates {
		if !s.GeneratedAt.After(*at) {
			return s, true
		}
	}
	return graphschema.Summary{}, false
}

func parseMaxAgeHours(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 24
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 24
	}
	return v
}

func annotateGraphFreshness(graph graphschema.Graph, evaluatedAt time.Time, maxAgeHours int) graphschema.Graph {
	if maxAgeHours <= 0 {
		maxAgeHours = 24
	}
	age := int64(0)
	if !graph.GeneratedAt.IsZero() && evaluatedAt.After(graph.GeneratedAt) {
		age = int64(evaluatedAt.Sub(graph.GeneratedAt).Seconds())
	}
	graph.Meta.Freshness = graphschema.GraphFreshness{
		EvaluatedAt: evaluatedAt.UTC(),
		MaxAgeHours: maxAgeHours,
		AgeSeconds:  age,
		IsStale:     age > int64(maxAgeHours*3600),
	}
	return graph
}

func parseGraphFilters(r *http.Request) (graphFilters, error) {
	confidenceMin, err := parseFloatDefault(r.URL.Query().Get("confidence_min"), 0)
	if err != nil {
		return graphFilters{}, err
	}
	return graphFilters{
		IncludeInferred: parseBoolDefault(r.URL.Query().Get("include_inferred"), false),
		EdgeTypeFilter:  strings.TrimSpace(r.URL.Query().Get("edge_types")),
		ServiceFilter:   strings.TrimSpace(r.URL.Query().Get("service")),
		RepoFilter:      strings.TrimSpace(r.URL.Query().Get("repo")),
		NodeFilter:      strings.TrimSpace(r.URL.Query().Get("node")),
		ConfidenceMin:   confidenceMin,
		Verification:    strings.TrimSpace(r.URL.Query().Get("verification_status")),
		ConflictStatus:  strings.TrimSpace(r.URL.Query().Get("conflict_status")),
		Environment:     strings.TrimSpace(r.URL.Query().Get("environment")),
		QueryText:       strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))),
	}, nil
}

func hasGraphFilter(f graphFilters) bool {
	return !f.IncludeInferred || f.EdgeTypeFilter != "" || f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.ConfidenceMin > 0 || f.Verification != "" || f.ConflictStatus != "" || f.Environment != "" || f.QueryText != ""
}

func filterGraph(graph graphschema.Graph, f graphFilters) graphschema.Graph {
	edges := filterGraphEdges(graph, f)
	serviceRepo := map[string]string{}
	for _, s := range graph.Meta.Services {
		serviceRepo[s.ID] = s.RepoPath
	}

	// If there is no explicit node-scoping filter and edge filtering produced no edges,
	// preserve nodes so node-only graphs remain visible in the UI.
	hasNodeScope := f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.Verification != "" || f.ConflictStatus != "" || f.Environment != "" || f.QueryText != ""
	if !hasNodeScope && len(edges) == 0 {
		graph.Edges = edges
		graph.Stats = recomputeGraphStats(graph.Nodes, edges)
		return graph
	}

	includeNodes := map[string]struct{}{}
	for _, e := range edges {
		includeNodes[e.SourceID] = struct{}{}
		includeNodes[e.TargetID] = struct{}{}
	}
	for _, n := range graph.Nodes {
		if f.ServiceFilter != "" && n.ServiceID == f.ServiceFilter {
			includeNodes[n.ID] = struct{}{}
		}
		if f.RepoFilter != "" && serviceRepo[n.ServiceID] == f.RepoFilter {
			includeNodes[n.ID] = struct{}{}
		}
		if f.NodeFilter != "" && n.ID == f.NodeFilter {
			includeNodes[n.ID] = struct{}{}
		}
	}

	nodes := make([]graphschema.Node, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if _, ok := includeNodes[n.ID]; !ok {
			continue
		}
		if !nodeMatchesFilters(n, serviceRepo, f) {
			continue
		}
		nodes = append(nodes, n)
	}
	nodeSet := map[string]struct{}{}
	for _, n := range nodes {
		nodeSet[n.ID] = struct{}{}
	}
	keptEdges := make([]graphschema.Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := nodeSet[e.SourceID]; !ok {
			continue
		}
		if _, ok := nodeSet[e.TargetID]; !ok {
			continue
		}
		keptEdges = append(keptEdges, e)
	}

	graph.Nodes = nodes
	graph.Edges = keptEdges
	graph.Stats = recomputeGraphStats(nodes, keptEdges)
	return graph
}

func filterGraphEdges(graph graphschema.Graph, f graphFilters) []graphschema.Edge {
	allowedTypes := map[string]struct{}{}
	if f.EdgeTypeFilter != "" {
		for _, t := range strings.Split(f.EdgeTypeFilter, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				allowedTypes[t] = struct{}{}
			}
		}
	}

	nodeService := map[string]string{}
	for _, n := range graph.Nodes {
		nodeService[n.ID] = n.ServiceID
	}
	serviceRepo := map[string]string{}
	for _, s := range graph.Meta.Services {
		serviceRepo[s.ID] = s.RepoPath
	}

	out := make([]graphschema.Edge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		if !f.IncludeInferred && e.Inferred {
			continue
		}
		if len(allowedTypes) > 0 {
			if _, ok := allowedTypes[e.Type]; !ok {
				continue
			}
		}
		if f.ServiceFilter != "" {
			if nodeService[e.SourceID] != f.ServiceFilter && nodeService[e.TargetID] != f.ServiceFilter {
				continue
			}
		}
		if f.RepoFilter != "" {
			sourceRepo := serviceRepo[nodeService[e.SourceID]]
			targetRepo := serviceRepo[nodeService[e.TargetID]]
			if sourceRepo != f.RepoFilter && targetRepo != f.RepoFilter {
				continue
			}
		}
		if e.Confidence < f.ConfidenceMin {
			continue
		}
		if f.NodeFilter != "" && e.SourceID != f.NodeFilter && e.TargetID != f.NodeFilter {
			continue
		}
		if f.QueryText != "" {
			blob := strings.ToLower(e.ID + " " + e.Type + " " + fmt.Sprint(e.Attributes))
			if !strings.Contains(blob, f.QueryText) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

func nodeMatchesFilters(n graphschema.Node, serviceRepo map[string]string, f graphFilters) bool {
	_ = serviceRepo
	if f.Verification != "" {
		v := strings.ToLower(strings.TrimSpace(fmt.Sprint(n.Attributes["verification_status"])))
		if v != strings.ToLower(f.Verification) {
			return false
		}
	}
	if f.ConflictStatus != "" {
		if n.Type != "conflict" {
			return false
		}
		status := strings.ToLower(strings.TrimSpace(fmt.Sprint(n.Attributes["status"])))
		if status != strings.ToLower(f.ConflictStatus) {
			return false
		}
	}
	if f.Environment != "" {
		env := strings.ToLower(strings.TrimSpace(fmt.Sprint(n.Attributes["environment"])))
		if env != strings.ToLower(f.Environment) {
			return false
		}
	}
	if f.QueryText != "" {
		blob := strings.ToLower(n.ID + " " + n.Type + " " + n.Label + " " + fmt.Sprint(n.Attributes))
		if !strings.Contains(blob, f.QueryText) {
			return false
		}
	}
	return true
}

func buildGraphExplain(graph graphschema.Graph) map[string]any {
	nodeExplain := make([]map[string]any, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeExplain = append(nodeExplain, map[string]any{
			"id":                  n.ID,
			"type":                n.Type,
			"label":               n.Label,
			"service_id":          n.ServiceID,
			"confidence":          n.Confidence,
			"inferred":            n.Inferred,
			"verification_status": n.Attributes["verification_status"],
			"environment":         n.Attributes["environment"],
			"status":              n.Attributes["status"],
		})
	}
	edgeExplain := make([]map[string]any, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		edgeExplain = append(edgeExplain, map[string]any{
			"id":            e.ID,
			"type":          e.Type,
			"source_id":     e.SourceID,
			"target_id":     e.TargetID,
			"confidence":    e.Confidence,
			"inferred":      e.Inferred,
			"attributes":    e.Attributes,
			"evidence_refs": e.EvidenceRefs,
		})
	}
	return map[string]any{
		"meta":  graph.Meta,
		"nodes": nodeExplain,
		"edges": edgeExplain,
	}
}

func recomputeGraphStats(nodes []graphschema.Node, edges []graphschema.Edge) graphschema.GraphStats {
	byNode := map[string]int{}
	byEdge := map[string]int{}
	for _, n := range nodes {
		byNode[n.Type]++
	}
	for _, e := range edges {
		byEdge[e.Type]++
	}
	return graphschema.GraphStats{
		NodeCount: len(nodes),
		EdgeCount: len(edges),
		ByNode:    byNode,
		ByEdge:    byEdge,
	}
}

func buildGraphMetrics(graph graphschema.Graph) graphMetrics {
	outgoing := map[string]int{}
	incoming := map[string]int{}
	labels := map[string]string{}
	edgeTypeCounts := map[string]int{}

	for _, n := range graph.Nodes {
		labels[n.ID] = n.Label
	}
	for _, e := range graph.Edges {
		outgoing[e.SourceID]++
		incoming[e.TargetID]++
		edgeTypeCounts[e.Type]++
	}

	return graphMetrics{
		GraphID:         graph.GraphID,
		NodeCount:       len(graph.Nodes),
		EdgeCount:       len(graph.Edges),
		EdgeTypeCounts:  edgeTypeCounts,
		TopCallers:      topMetricItems(outgoing, labels, 5),
		TopDependencies: topMetricItems(incoming, labels, 5),
	}
}

func topMetricItems(counts map[string]int, labels map[string]string, limit int) []metricItem {
	items := make([]metricItem, 0, len(counts))
	for nodeID, count := range counts {
		label := labels[nodeID]
		if label == "" {
			label = nodeID
		}
		items = append(items, metricItem{
			NodeID: nodeID,
			Label:  label,
			Count:  count,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].NodeID < items[j].NodeID
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func buildGraphCompare(from graphschema.Graph, to graphschema.Graph) graphCompare {
	fromNodes := map[string]graphschema.Node{}
	toNodes := map[string]graphschema.Node{}
	for _, n := range from.Nodes {
		fromNodes[n.ID] = n
	}
	for _, n := range to.Nodes {
		toNodes[n.ID] = n
	}

	fromEdges := map[string]graphschema.Edge{}
	toEdges := map[string]graphschema.Edge{}
	for _, e := range from.Edges {
		fromEdges[e.ID] = e
	}
	for _, e := range to.Edges {
		toEdges[e.ID] = e
	}

	addedNodes := make([]nodeCompareItem, 0)
	removedNodes := make([]nodeCompareItem, 0)
	changedNodes := make([]nodeChangeItem, 0)
	for id, n := range toNodes {
		if _, ok := fromNodes[id]; !ok {
			addedNodes = append(addedNodes, nodeCompareItem{
				ID:        n.ID,
				Type:      n.Type,
				Label:     n.Label,
				ServiceID: n.ServiceID,
			})
		}
	}
	for id, n := range fromNodes {
		if _, ok := toNodes[id]; !ok {
			removedNodes = append(removedNodes, nodeCompareItem{
				ID:        n.ID,
				Type:      n.Type,
				Label:     n.Label,
				ServiceID: n.ServiceID,
			})
		}
	}
	for id, fromNode := range fromNodes {
		toNode, ok := toNodes[id]
		if !ok {
			continue
		}
		keys := diffNodeKeys(fromNode, toNode)
		if len(keys) > 0 {
			changedNodes = append(changedNodes, nodeChangeItem{
				ID:        id,
				Label:     toNode.Label,
				ServiceID: toNode.ServiceID,
				Keys:      keys,
				Before: map[string]any{
					"type":       fromNode.Type,
					"label":      fromNode.Label,
					"service_id": fromNode.ServiceID,
					"confidence": fromNode.Confidence,
					"inferred":   fromNode.Inferred,
					"attributes": fromNode.Attributes,
				},
				After: map[string]any{
					"type":       toNode.Type,
					"label":      toNode.Label,
					"service_id": toNode.ServiceID,
					"confidence": toNode.Confidence,
					"inferred":   toNode.Inferred,
					"attributes": toNode.Attributes,
				},
			})
		}
	}

	addedEdges := make([]edgeCompareItem, 0)
	removedEdges := make([]edgeCompareItem, 0)
	changedEdges := make([]edgeChangeItem, 0)
	for id, e := range toEdges {
		if _, ok := fromEdges[id]; !ok {
			addedEdges = append(addedEdges, edgeCompareItem{
				ID:         e.ID,
				Type:       e.Type,
				SourceID:   e.SourceID,
				TargetID:   e.TargetID,
				Confidence: e.Confidence,
			})
		}
	}
	for id, e := range fromEdges {
		if _, ok := toEdges[id]; !ok {
			removedEdges = append(removedEdges, edgeCompareItem{
				ID:         e.ID,
				Type:       e.Type,
				SourceID:   e.SourceID,
				TargetID:   e.TargetID,
				Confidence: e.Confidence,
			})
		}
	}
	for id, fromEdge := range fromEdges {
		toEdge, ok := toEdges[id]
		if !ok {
			continue
		}
		keys := diffEdgeKeys(fromEdge, toEdge)
		if len(keys) > 0 {
			changedEdges = append(changedEdges, edgeChangeItem{
				ID:       id,
				Type:     toEdge.Type,
				SourceID: toEdge.SourceID,
				TargetID: toEdge.TargetID,
				Keys:     keys,
				Before: map[string]any{
					"type":       fromEdge.Type,
					"source_id":  fromEdge.SourceID,
					"target_id":  fromEdge.TargetID,
					"confidence": fromEdge.Confidence,
					"inferred":   fromEdge.Inferred,
					"attributes": fromEdge.Attributes,
				},
				After: map[string]any{
					"type":       toEdge.Type,
					"source_id":  toEdge.SourceID,
					"target_id":  toEdge.TargetID,
					"confidence": toEdge.Confidence,
					"inferred":   toEdge.Inferred,
					"attributes": toEdge.Attributes,
				},
			})
		}
	}

	sort.Slice(addedNodes, func(i, j int) bool { return addedNodes[i].ID < addedNodes[j].ID })
	sort.Slice(removedNodes, func(i, j int) bool { return removedNodes[i].ID < removedNodes[j].ID })
	sort.Slice(changedNodes, func(i, j int) bool { return changedNodes[i].ID < changedNodes[j].ID })
	sort.Slice(addedEdges, func(i, j int) bool { return addedEdges[i].ID < addedEdges[j].ID })
	sort.Slice(removedEdges, func(i, j int) bool { return removedEdges[i].ID < removedEdges[j].ID })
	sort.Slice(changedEdges, func(i, j int) bool { return changedEdges[i].ID < changedEdges[j].ID })

	return graphCompare{
		FromGraphID: from.GraphID,
		ToGraphID:   to.GraphID,
		FromCounts: map[string]int{
			"nodes": len(from.Nodes),
			"edges": len(from.Edges),
		},
		ToCounts: map[string]int{
			"nodes": len(to.Nodes),
			"edges": len(to.Edges),
		},
		AddedNodes:   addedNodes,
		RemovedNodes: removedNodes,
		ChangedNodes: changedNodes,
		AddedEdges:   addedEdges,
		RemovedEdges: removedEdges,
		ChangedEdges: changedEdges,
	}
}

func listCompareHistory(graphRoot string, r *http.Request, w http.ResponseWriter) {
	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, compareIndex{Compares: []compareSummary{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var index compareIndex
	if err := json.Unmarshal(data, &index); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode compare index: %v", err))
		return
	}
	if authCtx, err := security.ContextFromHeaders(r.Header); err == nil && !authCtx.HasRole("platform_admin") {
		filtered := make([]compareSummary, 0, len(index.Compares))
		for _, c := range index.Compares {
			if normalizeTenant(c.TenantID) == normalizeTenant(authCtx.TenantID) {
				filtered = append(filtered, c)
			}
		}
		index.Compares = filtered
	}
	// Optional cap to avoid sending very large history lists.
	limit := 0
	if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	before := strings.TrimSpace(r.URL.Query().Get("before"))
	start := 0
	if before != "" {
		found := -1
		for i, c := range index.Compares {
			if c.CompareID == before {
				found = i
				break
			}
		}
		if found == -1 {
			writeError(w, http.StatusBadRequest, "invalid before cursor")
			return
		}
		start = found + 1
	}
	if start > len(index.Compares) {
		start = len(index.Compares)
	}
	comps := index.Compares[start:]
	nextBefore := ""
	if limit > 0 && len(comps) > limit {
		comps = comps[:limit]
		nextBefore = comps[len(comps)-1].CompareID
	}
	writeJSON(w, http.StatusOK, compareIndex{
		Compares:   comps,
		NextBefore: nextBefore,
	})
}

func persistCompareResult(graphRoot string, result *graphCompare) error {
	if result == nil {
		return fmt.Errorf("nil compare result")
	}
	now := time.Now().UTC()
	if result.CompareID == "" {
		result.CompareID = fmt.Sprintf("%d", now.UnixNano())
	}
	result.GeneratedAt = now

	compareDir := filepath.Join(graphRoot, "compare", result.CompareID)
	if err := os.MkdirAll(compareDir, 0o755); err != nil {
		return fmt.Errorf("create compare dir: %w", err)
	}
	comparePath := filepath.Join(compareDir, "compare.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal compare result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(comparePath, data, 0o644); err != nil {
		return fmt.Errorf("write compare result: %w", err)
	}

	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	idx := compareIndex{Compares: []compareSummary{}}
	if indexData, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(indexData, &idx)
	}
	filtered := make([]compareSummary, 0, len(idx.Compares)+1)
	for _, c := range idx.Compares {
		if c.CompareID != result.CompareID {
			filtered = append(filtered, c)
		}
	}
	filtered = append(filtered, compareSummary{
		CompareID:    result.CompareID,
		GeneratedAt:  result.GeneratedAt,
		TenantID:     normalizeTenant(result.TenantID),
		FromGraphID:  result.FromGraphID,
		ToGraphID:    result.ToGraphID,
		AddedNodes:   len(result.AddedNodes),
		RemovedNodes: len(result.RemovedNodes),
		ChangedNodes: len(result.ChangedNodes),
		AddedEdges:   len(result.AddedEdges),
		RemovedEdges: len(result.RemovedEdges),
		ChangedEdges: len(result.ChangedEdges),
		Path:         comparePath,
	})
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].GeneratedAt.After(filtered[j].GeneratedAt)
	})
	idx.Compares = filtered

	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal compare index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return fmt.Errorf("create compare index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write compare index: %w", err)
	}
	return nil
}

func loadCompareResult(graphRoot string, compareID string) (graphCompare, error) {
	comparePath := filepath.Join(graphRoot, "compare", compareID, "compare.json")
	data, err := os.ReadFile(comparePath)
	if err != nil {
		return graphCompare{}, err
	}
	var payload graphCompare
	if err := json.Unmarshal(data, &payload); err != nil {
		return graphCompare{}, fmt.Errorf("decode compare result: %w", err)
	}
	return payload, nil
}

func deleteCompareResult(graphRoot string, compareID string) error {
	comparePath := filepath.Join(graphRoot, "compare", compareID, "compare.json")
	if _, err := os.Stat(comparePath); err != nil {
		return err
	}
	compareDir := filepath.Dir(comparePath)
	if err := os.RemoveAll(compareDir); err != nil {
		return fmt.Errorf("remove compare dir: %w", err)
	}

	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	idx := compareIndex{Compares: []compareSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &idx)
	}
	filtered := make([]compareSummary, 0, len(idx.Compares))
	for _, c := range idx.Compares {
		if c.CompareID != compareID {
			filtered = append(filtered, c)
		}
	}
	idx.Compares = filtered
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal compare index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write compare index: %w", err)
	}
	return nil
}

func pruneCompareHistory(graphRoot string, keepLatest int) (int, error) {
	indexPath := filepath.Join(graphRoot, "compare", "index.json")
	idx := compareIndex{Compares: []compareSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		if err := json.Unmarshal(data, &idx); err != nil {
			return 0, fmt.Errorf("decode compare index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	if keepLatest >= len(idx.Compares) {
		return 0, nil
	}

	toDelete := idx.Compares[keepLatest:]
	deleted := 0
	for _, c := range toDelete {
		if c.Path == "" {
			continue
		}
		if err := os.RemoveAll(filepath.Dir(c.Path)); err == nil {
			deleted++
		}
	}
	idx.Compares = idx.Compares[:keepLatest]
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return deleted, fmt.Errorf("marshal compare index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return deleted, fmt.Errorf("create compare index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return deleted, fmt.Errorf("write compare index: %w", err)
	}
	return deleted, nil
}

func handleAuditList(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot)
		if !ok {
			return
		}
		limit := 200
		if q := strings.TrimSpace(r.URL.Query().Get("limit")); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "invalid limit")
				return
			}
			limit = n
		}
		tenant := normalizeTenant(authCtx.TenantID)
		if authCtx.HasRole("platform_admin") {
			tenant = ""
		}
		events, err := audit.ListEvents(filepath.Dir(graphRoot), tenant, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"count":  len(events),
			"events": events,
		})
	}
}

func handleAuditExport(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditExport, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		var req struct {
			From    string `json:"from"`
			To      string `json:"to"`
			Encrypt bool   `json:"encrypt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		var fromPtr *time.Time
		if strings.TrimSpace(req.From) != "" {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(req.From))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid from timestamp")
				return
			}
			u := ts.UTC()
			fromPtr = &u
		}
		var toPtr *time.Time
		if strings.TrimSpace(req.To) != "" {
			ts, err := time.Parse(time.RFC3339, strings.TrimSpace(req.To))
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid to timestamp")
				return
			}
			u := ts.UTC()
			toPtr = &u
		}
		keyID := strings.TrimSpace(os.Getenv("DIFFMIND_KMS_KEY_ID"))
		result, err := audit.ExportEvents(filepath.Dir(graphRoot), audit.ExportRequest{
			From:     fromPtr,
			To:       toPtr,
			TenantID: normalizeTenant(authCtx.TenantID),
			Encrypt:  req.Encrypt,
			KeyB64:   strings.TrimSpace(os.Getenv("DIFFMIND_AUDIT_EXPORT_KEY_B64")),
			KeyID:    keyID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionAuditExport, "allow", "audit_exported", map[string]any{
			"path":      result.Path,
			"encrypted": result.Encrypted,
			"count":     result.Count,
		})
		writeJSON(w, http.StatusOK, result)
	}
}

func handleAuditRetention(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionAuditPrune, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot)
		if !ok {
			return
		}
		var req struct {
			RetainDays int `json:"retain_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		if req.RetainDays <= 0 {
			writeError(w, http.StatusBadRequest, "retain_days must be > 0")
			return
		}
		cutoff := time.Now().UTC().Add(-time.Duration(req.RetainDays) * 24 * time.Hour)
		deleted, err := audit.PruneEvents(filepath.Dir(graphRoot), normalizeTenant(authCtx.TenantID), cutoff)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		recordAudit(graphRoot, authCtx, r, security.ActionAuditPrune, "allow", "audit_pruned", map[string]any{"deleted": deleted, "retain_days": req.RetainDays})
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted":     deleted,
			"retain_days": req.RetainDays,
		})
	}
}

func handleProductPRReview(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req productPRReviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.GraphID = strings.TrimSpace(req.GraphID)
		if req.GraphID == "" {
			writeError(w, http.StatusBadRequest, "graph_id is required")
			return
		}
		if req.MaxFindings <= 0 {
			req.MaxFindings = 50
		}
		graph, err := loadGraph(graphRoot, req.GraphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), true, false, graphRoot)
		if !ok {
			return
		}
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)

		changedSet := map[string]struct{}{}
		for _, n := range req.ChangedNodes {
			n = strings.TrimSpace(n)
			if n != "" {
				changedSet[n] = struct{}{}
			}
		}
		findings := make([]productFinding, 0)
		for _, e := range graph.Edges {
			if len(changedSet) > 0 {
				_, src := changedSet[e.SourceID]
				_, dst := changedSet[e.TargetID]
				if !src && !dst {
					continue
				}
			}
			if status := strings.ToLower(strings.TrimSpace(fmt.Sprint(e.Attributes["verification_status"]))); status == "rejected" {
				findings = append(findings, productFinding{
					Severity: "high",
					Title:    "Rejected relation involved in change scope",
					EdgeID:   e.ID,
					Details: map[string]any{
						"type":      e.Type,
						"source_id": e.SourceID,
						"target_id": e.TargetID,
					},
				})
			}
			if e.Confidence < 0.70 {
				findings = append(findings, productFinding{
					Severity: "medium",
					Title:    "Low-confidence relation touched by PR",
					EdgeID:   e.ID,
					Details: map[string]any{
						"type":       e.Type,
						"confidence": e.Confidence,
					},
				})
			}
		}
		for _, n := range graph.Nodes {
			if len(changedSet) > 0 {
				if _, ok := changedSet[n.ID]; !ok {
					continue
				}
			}
			if strings.EqualFold(strings.TrimSpace(fmt.Sprint(n.Attributes["verification_status"])), "needs_review") {
				findings = append(findings, productFinding{
					Severity: "medium",
					Title:    "Changed entity requires verification review",
					NodeID:   n.ID,
					Details: map[string]any{
						"type":   n.Type,
						"label":  n.Label,
						"status": n.Attributes["verification_status"],
					},
				})
			}
		}
		sort.Slice(findings, func(i, j int) bool {
			if findings[i].Severity == findings[j].Severity {
				return findings[i].Title < findings[j].Title
			}
			return findings[i].Severity > findings[j].Severity
		})
		if len(findings) > req.MaxFindings {
			findings = findings[:req.MaxFindings]
		}

		resp := productPRReviewResponse{
			GraphID:  graph.GraphID,
			Findings: findings,
			Summary: map[string]int{
				"high":   countSeverity(findings, "high"),
				"medium": countSeverity(findings, "medium"),
				"low":    countSeverity(findings, "low"),
			},
			Generated: time.Now().UTC(),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph_id":       graph.GraphID,
				"changed_nodes":  req.ChangedNodes,
				"findings":       findings,
			},
		})
	}
}

func handleProductDocs(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/docs/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		includeSensitive := parseBoolDefault(r.URL.Query().Get("include_sensitive"), false)
		graph = security.RedactGraph(graph, authCtx, includeSensitive)

		serviceFilter := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceFilter != "" {
			graph = filterGraph(graph, graphFilters{ServiceFilter: serviceFilter, IncludeInferred: true})
		}
		doc := map[string]any{
			"graph_id":   graph.GraphID,
			"service":    serviceFilter,
			"generated":  time.Now().UTC(),
			"overview":   buildDocsOverview(graph),
			"operations": buildDocsOperations(graph),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, doc)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": doc,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph":          graph,
			},
		})
	}
}

func handleProductMapper(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/mapper/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		serviceID := strings.TrimSpace(r.URL.Query().Get("service"))
		if serviceID == "" {
			writeError(w, http.StatusBadRequest, "service query param is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), false, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		subgraph := buildServiceImpactSubgraph(graph, serviceID)
		resp := map[string]any{
			"graph_id":      graph.GraphID,
			"service":       serviceID,
			"impact_graph":  subgraph,
			"impact_counts": map[string]int{"nodes": len(subgraph.Nodes), "edges": len(subgraph.Edges)},
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": resp,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"selection":      "service impact subgraph by service_id",
			},
		})
	}
}

func handleProductGovernance(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		graphID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/products/governance/"))
		graphID = strings.Trim(graphID, "/")
		if graphID == "" {
			writeError(w, http.StatusBadRequest, "graph id is required")
			return
		}
		graph, err := loadGraph(graphRoot, graphID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "graph not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, ok := authorizeRequest(w, r, security.ActionQueryGraph, graphTenant(graph), true, false, graphRoot)
		if !ok {
			return
		}
		graph = security.RedactGraph(graph, authCtx, parseBoolDefault(r.URL.Query().Get("include_sensitive"), false))
		report := map[string]any{
			"graph_id":                  graph.GraphID,
			"generated_at":              time.Now().UTC(),
			"risk_posture":              buildGovernancePosture(graph),
			"open_conflicts":            countNodesByType(graph.Nodes, "conflict"),
			"sensitive_surfaces":        countNodesByType(graph.Nodes, "sensitive_surface"),
			"verification_needs_review": countNodesByAttr(graph.Nodes, "verification_status", "needs_review"),
		}
		explain := parseBoolDefault(r.URL.Query().Get("explain"), false)
		if !explain {
			writeJSON(w, http.StatusOK, report)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"result": report,
			"explain": map[string]any{
				"query_contract": "graph_query",
				"graph_stats":    graph.Stats,
			},
		})
	}
}

func (m *runtimeMetrics) record(route string, status int, d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rm, ok := m.routes[route]
	if !ok {
		rm = &routeMetrics{Route: route}
		m.routes[route] = rm
	}
	rm.Requests++
	if status >= 500 {
		rm.Errors++
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		rm.AuthFailures++
	}
	rm.LastStatus = status
	rm.LastAccessedAt = time.Now().UTC()
	ms := float64(d.Milliseconds())
	if ms <= 0 {
		ms = float64(d.Microseconds()) / 1000.0
	}
	rm.TotalDurationMS += ms
	// Exponential approximation for p95 that is stable in-memory.
	if rm.P95DurationMS == 0 || ms > rm.P95DurationMS {
		rm.P95DurationMS = ms
	} else {
		rm.P95DurationMS = (rm.P95DurationMS*0.95 + ms*0.05)
	}
	success := rm.Requests - rm.Errors - rm.AuthFailures
	if rm.Requests > 0 {
		rm.AvailabilityRate = (float64(success) / float64(rm.Requests)) * 100
	}
}

func (m *runtimeMetrics) snapshot() []routeMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]routeMetrics, 0, len(m.routes))
	for _, r := range m.routes {
		copyR := *r
		out = append(out, copyR)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

func handleOpsMetrics(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		snap := apiMetrics.snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"generated_at": time.Now().UTC(),
			"routes":       snap,
		})
	}
}

func handleOpsSLO(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		routes := apiMetrics.snapshot()
		total := int64(0)
		success := int64(0)
		p95 := 0.0
		for _, rm := range routes {
			critical := strings.HasPrefix(rm.Route, "/graphs") || strings.HasPrefix(rm.Route, "/products")
			if !critical {
				continue
			}
			total += rm.Requests
			success += rm.Requests - rm.Errors - rm.AuthFailures
			if rm.P95DurationMS > p95 {
				p95 = rm.P95DurationMS
			}
		}
		adherence := 100.0
		if total > 0 {
			adherence = (float64(success) / float64(total)) * 100
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"generated_at":            time.Now().UTC(),
			"critical_requests":       total,
			"critical_successes":      success,
			"slo_adherence":           adherence,
			"slo_target":              99.9,
			"slo_passed":              adherence >= 99.9,
			"critical_latency_p95_ms": p95,
			"routes":                  routes,
		})
	}
}

func countSeverity(findings []productFinding, severity string) int {
	n := 0
	for _, f := range findings {
		if strings.EqualFold(f.Severity, severity) {
			n++
		}
	}
	return n
}

func buildDocsOverview(graph graphschema.Graph) map[string]any {
	return map[string]any{
		"services":        countNodesByType(graph.Nodes, "service"),
		"endpoints":       countNodesByType(graph.Nodes, "endpoint"),
		"queues":          countNodesByType(graph.Nodes, "queue"),
		"databases":       countNodesByType(graph.Nodes, "database"),
		"dependencies":    countNodesByType(graph.Nodes, "dependency"),
		"deployments":     countNodesByType(graph.Nodes, "deployment"),
		"infra_resources": countNodesByType(graph.Nodes, "infra_resource"),
	}
}

func buildDocsOperations(graph graphschema.Graph) map[string]any {
	return map[string]any{
		"top_callers":      topMetricItems(countOutgoing(graph.Edges), nodeLabels(graph.Nodes), 10),
		"top_dependencies": topMetricItems(countIncoming(graph.Edges), nodeLabels(graph.Nodes), 10),
		"edge_types":       graph.Stats.ByEdge,
	}
}

func buildServiceImpactSubgraph(graph graphschema.Graph, serviceID string) graphschema.Graph {
	includeNodes := map[string]struct{}{}
	for _, n := range graph.Nodes {
		if n.ServiceID == serviceID || n.ID == serviceID {
			includeNodes[n.ID] = struct{}{}
		}
	}
	changed := true
	for changed {
		changed = false
		for _, e := range graph.Edges {
			_, src := includeNodes[e.SourceID]
			_, dst := includeNodes[e.TargetID]
			if src || dst {
				if _, ok := includeNodes[e.SourceID]; !ok {
					includeNodes[e.SourceID] = struct{}{}
					changed = true
				}
				if _, ok := includeNodes[e.TargetID]; !ok {
					includeNodes[e.TargetID] = struct{}{}
					changed = true
				}
			}
		}
	}
	nodes := make([]graphschema.Node, 0)
	for _, n := range graph.Nodes {
		if _, ok := includeNodes[n.ID]; ok {
			nodes = append(nodes, n)
		}
	}
	edges := make([]graphschema.Edge, 0)
	for _, e := range graph.Edges {
		_, src := includeNodes[e.SourceID]
		_, dst := includeNodes[e.TargetID]
		if src && dst {
			edges = append(edges, e)
		}
	}
	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
	return graph
}

func buildGovernancePosture(graph graphschema.Graph) string {
	highRisk := countNodesByType(graph.Nodes, "dependency_risk")
	openConflicts := countNodesByType(graph.Nodes, "conflict")
	needsReview := countNodesByAttr(graph.Nodes, "verification_status", "needs_review")
	if highRisk+openConflicts+needsReview > 20 {
		return "high_risk"
	}
	if highRisk+openConflicts+needsReview > 5 {
		return "medium_risk"
	}
	return "low_risk"
}

func countNodesByType(nodes []graphschema.Node, typ string) int {
	n := 0
	for _, node := range nodes {
		if node.Type == typ {
			n++
		}
	}
	return n
}

func countNodesByAttr(nodes []graphschema.Node, key string, value string) int {
	n := 0
	for _, node := range nodes {
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(node.Attributes[key])), value) {
			n++
		}
	}
	return n
}

func nodeLabels(nodes []graphschema.Node) map[string]string {
	out := map[string]string{}
	for _, n := range nodes {
		out[n.ID] = n.Label
	}
	return out
}

func countOutgoing(edges []graphschema.Edge) map[string]int {
	out := map[string]int{}
	for _, e := range edges {
		out[e.SourceID]++
	}
	return out
}

func countIncoming(edges []graphschema.Edge) map[string]int {
	out := map[string]int{}
	for _, e := range edges {
		out[e.TargetID]++
	}
	return out
}

func diffNodeKeys(from graphschema.Node, to graphschema.Node) []string {
	keys := make([]string, 0, 6)
	if from.Type != to.Type {
		keys = append(keys, "type")
	}
	if from.Label != to.Label {
		keys = append(keys, "label")
	}
	if from.ServiceID != to.ServiceID {
		keys = append(keys, "service_id")
	}
	if from.Confidence != to.Confidence {
		keys = append(keys, "confidence")
	}
	if from.Inferred != to.Inferred {
		keys = append(keys, "inferred")
	}
	if !jsonEqual(from.Attributes, to.Attributes) {
		keys = append(keys, "attributes")
	}
	return keys
}

func diffEdgeKeys(from graphschema.Edge, to graphschema.Edge) []string {
	keys := make([]string, 0, 7)
	if from.Type != to.Type {
		keys = append(keys, "type")
	}
	if from.SourceID != to.SourceID {
		keys = append(keys, "source_id")
	}
	if from.TargetID != to.TargetID {
		keys = append(keys, "target_id")
	}
	if from.Confidence != to.Confidence {
		keys = append(keys, "confidence")
	}
	if from.Inferred != to.Inferred {
		keys = append(keys, "inferred")
	}
	if !jsonEqual(from.Attributes, to.Attributes) {
		keys = append(keys, "attributes")
	}
	return keys
}

func jsonEqual(a any, b any) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aj) == string(bj)
}

func parseBoolDefault(v string, d bool) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return d
	}
	return v == "1" || v == "true" || v == "yes"
}

func parseFloatDefault(v string, d float64) (float64, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return d, nil
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, err
	}
	return f, nil
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": message,
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
