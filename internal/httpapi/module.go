package httpapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"diffmind/internal/audit"
	"diffmind/internal/bundleio"
	"diffmind/internal/contracts"
	"diffmind/internal/diff"
	"diffmind/internal/finalgate"
	graphpkg "diffmind/internal/graph"
	"diffmind/internal/graphschema"
	"diffmind/internal/query"
	runtimepkg "diffmind/internal/runtime"
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
	SectionFilter   string
	ClassFilter     string
	ConfidenceMin   float64
	Verification    string
	AdapterID       string
	ProvVersion     string
	ConflictStatus  string
	Environment     string
	QueryText       string
}

type publishPolicy struct {
	IncludeDisputed bool
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

type runtimeReconcileSummary struct {
	ReconcileID  string    `json:"reconcile_id"`
	GeneratedAt  time.Time `json:"generated_at"`
	TenantID     string    `json:"tenant_id,omitempty"`
	GraphID      string    `json:"graph_id"`
	Claims       int       `json:"claims"`
	Observations int       `json:"observations"`
	Confirmed    int       `json:"confirmed"`
	Contradicted int       `json:"contradicted"`
	Unmapped     int       `json:"runtime_only_unmapped"`
	NeedsReview  int       `json:"needs_review"`
	Path         string    `json:"path"`
}

type runtimeReconcileIndex struct {
	Runs       []runtimeReconcileSummary `json:"runs"`
	NextBefore string                    `json:"next_before,omitempty"`
}

type runtimeReconcileRecord struct {
	ReconcileID string                                 `json:"reconcile_id"`
	GeneratedAt time.Time                              `json:"generated_at"`
	TenantID    string                                 `json:"tenant_id,omitempty"`
	Request     contracts.RuntimeReconciliationRequest `json:"request"`
	Result      contracts.RuntimeReconciliationResult  `json:"result"`
}

type runtimeReconcileCompare struct {
	FromReconcileID     string   `json:"from_reconcile_id"`
	ToReconcileID       string   `json:"to_reconcile_id"`
	FromGraphID         string   `json:"from_graph_id"`
	ToGraphID           string   `json:"to_graph_id"`
	ConfirmedAdded      []string `json:"confirmed_added"`
	ConfirmedRemoved    []string `json:"confirmed_removed"`
	ContradictedAdded   []string `json:"contradicted_added"`
	ContradictedRemoved []string `json:"contradicted_removed"`
	UnmappedAdded       []string `json:"runtime_only_unmapped_added"`
	UnmappedRemoved     []string `json:"runtime_only_unmapped_removed"`
	NeedsReviewAdded    []string `json:"needs_review_added"`
	NeedsReviewRemoved  []string `json:"needs_review_removed"`
}

type runtimeReconcileGraphSummary struct {
	GraphID      string    `json:"graph_id"`
	Runs         int       `json:"runs"`
	Claims       int       `json:"claims"`
	Observations int       `json:"observations"`
	Confirmed    int       `json:"confirmed"`
	Contradicted int       `json:"contradicted"`
	Unmapped     int       `json:"runtime_only_unmapped"`
	NeedsReview  int       `json:"needs_review"`
	LastRunAt    time.Time `json:"last_run_at"`
}

type runtimeReconcileReport struct {
	GeneratedAt       time.Time                      `json:"generated_at"`
	TenantID          string                         `json:"tenant_id,omitempty"`
	GraphID           string                         `json:"graph_id,omitempty"`
	From              string                         `json:"from,omitempty"`
	To                string                         `json:"to,omitempty"`
	TotalRuns         int                            `json:"total_runs"`
	TotalClaims       int                            `json:"total_claims"`
	TotalObservations int                            `json:"total_observations"`
	TotalConfirmed    int                            `json:"total_confirmed"`
	TotalContradicted int                            `json:"total_contradicted"`
	TotalUnmapped     int                            `json:"total_runtime_only_unmapped"`
	TotalNeedsReview  int                            `json:"total_needs_review"`
	ConfirmedRate     float64                        `json:"confirmed_rate"`
	ContradictedRate  float64                        `json:"contradicted_rate"`
	NeedsReviewRate   float64                        `json:"needs_review_rate"`
	UnmappedRate      float64                        `json:"runtime_only_unmapped_rate"`
	TopGraphs         []runtimeReconcileGraphSummary `json:"top_graphs"`
	LatestRun         *runtimeReconcileSummary       `json:"latest_run,omitempty"`
}

type finalGateAttestRequest struct {
	QualityGatePath string   `json:"quality_gate_path"`
	SLOPath         string   `json:"slo_path"`
	TemplatesPath   string   `json:"templates_path"`
	CatalogPath     string   `json:"catalog_path"`
	GraphIndexPath  string   `json:"graph_index_path"`
	OutReportPath   string   `json:"out_report_path"`
	OutDecisionPath string   `json:"out_decision_path"`
	Signers         []string `json:"signers"`
}

type productTemplate struct {
	ID      string         `json:"id"`
	Product string         `json:"product"`
	Method  string         `json:"method"`
	Path    string         `json:"path"`
	Query   map[string]any `json:"query,omitempty"`
	Payload any            `json:"payload,omitempty"`
}

type productTemplateFile struct {
	Templates []productTemplate `json:"templates"`
}

type productTemplateExecuteRequest struct {
	TemplateID    string         `json:"template_id"`
	TemplatePath  string         `json:"template_path,omitempty"`
	Vars          map[string]any `json:"vars,omitempty"`
	IncludeResult bool           `json:"include_result,omitempty"`
}

type productQuestionFile struct {
	Questions []struct {
		ID       string `json:"id"`
		Question string `json:"question"`
		Endpoint string `json:"endpoint"`
	} `json:"questions"`
}

type productQuestionExecuteRequest struct {
	QuestionID   string         `json:"question_id"`
	CatalogPath  string         `json:"catalog_path,omitempty"`
	TemplatePath string         `json:"template_path,omitempty"`
	Vars         map[string]any `json:"vars,omitempty"`
}

type productQuestionRunRequest struct {
	QuestionIDs    []string                  `json:"question_ids,omitempty"`
	CatalogPath    string                    `json:"catalog_path,omitempty"`
	TemplatePath   string                    `json:"template_path,omitempty"`
	Vars           map[string]any            `json:"vars,omitempty"`
	VarsByQuestion map[string]map[string]any `json:"vars_by_question,omitempty"`
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
	mux.Handle("/products/templates", instrument("/products/templates", handleProductTemplates(graphRoot)))
	mux.Handle("/products/templates/execute", instrument("/products/templates/execute", handleProductTemplateExecute(graphRoot)))
	mux.Handle("/products/questions", instrument("/products/questions", handleProductQuestions(graphRoot)))
	mux.Handle("/products/questions/execute", instrument("/products/questions/execute", handleProductQuestionExecute(graphRoot)))
	mux.Handle("/products/questions/run", instrument("/products/questions/run", handleProductQuestionRun(graphRoot)))
	mux.Handle("/products/questions/coverage", instrument("/products/questions/coverage", handleProductQuestionCoverage(graphRoot)))
	mux.Handle("/products/pr-review", instrument("/products/pr-review", handleProductPRReview(graphRoot)))
	mux.Handle("/products/docs/", instrument("/products/docs/:graph_id", handleProductDocs(graphRoot)))
	mux.Handle("/products/mapper/", instrument("/products/mapper/:graph_id", handleProductMapper(graphRoot)))
	mux.Handle("/products/governance/", instrument("/products/governance/:graph_id", handleProductGovernance(graphRoot)))
	mux.Handle("/ops/metrics", instrument("/ops/metrics", handleOpsMetrics(graphRoot)))
	mux.Handle("/ops/slo", instrument("/ops/slo", handleOpsSLO(graphRoot)))
	mux.Handle("/final/attest", instrument("/final/attest", handleFinalGateAttest(graphRoot)))
	mux.Handle("/final/readiness", instrument("/final/readiness", handleFinalReadiness(graphRoot)))
	mux.Handle("/final/decision", instrument("/final/decision", handleFinalDecision(graphRoot)))
	mux.Handle("/runtime/plan", instrument("/runtime/plan", handleRuntimePlan(graphRoot)))
	mux.Handle("/runtime/reconcile", instrument("/runtime/reconcile", handleRuntimeReconcile(graphRoot)))
	mux.Handle("/runtime/reconcile/report", instrument("/runtime/reconcile/report", handleRuntimeReconcileReport(graphRoot)))
	mux.Handle("/runtime/reconcile/compare", instrument("/runtime/reconcile/compare", handleRuntimeReconcileCompare(graphRoot)))
	mux.Handle("/runtime/reconcile/", instrument("/runtime/reconcile/:id", handleRuntimeReconcileByID(graphRoot)))
	mux.Handle("/runtime/claims/", instrument("/runtime/claims/:graph_id", handleRuntimeClaims(graphRoot)))
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
		policy := parsePublishPolicy(r)
		fromGraph = applyStrictPublishPolicy(fromGraph, policy)
		toGraph = applyStrictPublishPolicy(toGraph, policy)
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
		graph = applyStrictPublishPolicy(graph, parsePublishPolicy(r))
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
		if len(parts) == 2 && parts[1] == "summary" {
			writeJSON(w, http.StatusOK, buildGraphSummary(graph))
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
		graph = applyStrictPublishPolicy(graph, parsePublishPolicy(r))
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
		SectionFilter:   strings.TrimSpace(r.URL.Query().Get("section")),
		ClassFilter:     strings.TrimSpace(r.URL.Query().Get("class")),
		ConfidenceMin:   confidenceMin,
		Verification:    firstNonEmptyTrimmed(r.URL.Query().Get("verification_state"), r.URL.Query().Get("verification_status")),
		AdapterID:       strings.TrimSpace(r.URL.Query().Get("adapter_id")),
		ProvVersion:     strings.TrimSpace(r.URL.Query().Get("provenance_version")),
		ConflictStatus:  strings.TrimSpace(r.URL.Query().Get("conflict_status")),
		Environment:     strings.TrimSpace(r.URL.Query().Get("environment")),
		QueryText:       strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))),
	}, nil
}

func parsePublishPolicy(r *http.Request) publishPolicy {
	return publishPolicy{
		IncludeDisputed: parseBoolDefault(r.URL.Query().Get("include_disputed"), false),
	}
}

func hasGraphFilter(f graphFilters) bool {
	return !f.IncludeInferred || f.EdgeTypeFilter != "" || f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.SectionFilter != "" || f.ClassFilter != "" || f.ConfidenceMin > 0 || f.Verification != "" || f.AdapterID != "" || f.ProvVersion != "" || f.ConflictStatus != "" || f.Environment != "" || f.QueryText != ""
}

func filterGraph(graph graphschema.Graph, f graphFilters) graphschema.Graph {
	edges := filterGraphEdges(graph, f)
	serviceRepo := map[string]string{}
	for _, s := range graph.Meta.Services {
		serviceRepo[s.ID] = s.RepoPath
	}

	// If there is no explicit node-scoping filter and edge filtering produced no edges,
	// preserve nodes so node-only graphs remain visible in the UI.
	hasNodeScope := f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.SectionFilter != "" || f.ClassFilter != "" || f.Verification != "" || f.AdapterID != "" || f.ProvVersion != "" || f.ConflictStatus != "" || f.Environment != "" || f.QueryText != ""
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
	if hasNodeScope {
		for _, n := range graph.Nodes {
			if nodeMatchesFilters(n, serviceRepo, f) {
				includeNodes[n.ID] = struct{}{}
			}
		}
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
		if f.SectionFilter != "" && !equalsFoldTrimmed(e.Section, f.SectionFilter) {
			continue
		}
		if f.ClassFilter != "" && !equalsFoldTrimmed(e.Class, f.ClassFilter) {
			continue
		}
		if f.Verification != "" {
			v := effectiveVerificationState(e.VerificationState, e.Attributes, e.Inferred)
			if !equalsFoldTrimmed(v, f.Verification) {
				continue
			}
		}
		if f.AdapterID != "" {
			adapter := firstNonEmptyTrimmed(
				attrString(e.Attributes, "adapter_id"),
				attrString(e.Attributes, "provenance_adapter_id"),
			)
			if !equalsFoldTrimmed(adapter, f.AdapterID) {
				continue
			}
		}
		if f.ProvVersion != "" {
			version := firstNonEmptyTrimmed(
				attrString(e.Attributes, "provenance_version"),
				attrString(e.Attributes, "adapter_version"),
				attrString(e.Attributes, "rulepack_version"),
			)
			if !equalsFoldTrimmed(version, f.ProvVersion) {
				continue
			}
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
	if f.SectionFilter != "" && !equalsFoldTrimmed(n.Section, f.SectionFilter) {
		return false
	}
	if f.ClassFilter != "" && !equalsFoldTrimmed(n.Class, f.ClassFilter) {
		return false
	}
	if f.Verification != "" {
		v := effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred)
		if !equalsFoldTrimmed(v, f.Verification) {
			return false
		}
	}
	if f.AdapterID != "" {
		adapter := firstNonEmptyTrimmed(
			attrString(n.Attributes, "adapter_id"),
			attrString(n.Attributes, "provenance_adapter_id"),
		)
		if !equalsFoldTrimmed(adapter, f.AdapterID) {
			return false
		}
	}
	if f.ProvVersion != "" {
		version := firstNonEmptyTrimmed(
			attrString(n.Attributes, "provenance_version"),
			attrString(n.Attributes, "adapter_version"),
			attrString(n.Attributes, "rulepack_version"),
		)
		if !equalsFoldTrimmed(version, f.ProvVersion) {
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

func applyStrictPublishPolicy(graph graphschema.Graph, policy publishPolicy) graphschema.Graph {
	keepNode := map[string]struct{}{}
	for _, n := range graph.Nodes {
		if shouldKeepByPublishPolicy(n.Section, n.VerificationState, n.Attributes, n.Inferred, policy) {
			keepNode[n.ID] = struct{}{}
		}
	}

	nodes := make([]graphschema.Node, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if _, ok := keepNode[n.ID]; !ok {
			continue
		}
		nodes = append(nodes, n)
	}

	edges := make([]graphschema.Edge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		if _, ok := keepNode[e.SourceID]; !ok {
			continue
		}
		if _, ok := keepNode[e.TargetID]; !ok {
			continue
		}
		if !shouldKeepByPublishPolicy(e.Section, e.VerificationState, e.Attributes, e.Inferred, policy) {
			continue
		}
		edges = append(edges, e)
	}

	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
	return graph
}

func shouldKeepByPublishPolicy(section string, verificationState string, attrs map[string]any, inferred bool, policy publishPolicy) bool {
	sec := strings.ToLower(strings.TrimSpace(section))
	if sec != "exposure" && sec != "dependencies" {
		return true
	}
	state := effectiveVerificationState(verificationState, attrs, inferred)
	if state == "verified" {
		return true
	}
	if state == "disputed" && policy.IncludeDisputed {
		return true
	}
	return false
}

func effectiveVerificationState(field string, attrs map[string]any, inferred bool) string {
	if inferred {
		return "inferred"
	}
	if v := strings.ToLower(strings.TrimSpace(field)); v != "" {
		return v
	}
	if attrs != nil {
		if raw, ok := attrs["verification_state"]; ok {
			if v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))); v != "" {
				return v
			}
		}
		if raw, ok := attrs["verification_status"]; ok {
			if v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))); v != "" {
				return v
			}
		}
		if raw, ok := attrs["status"]; ok {
			if v := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw))); v != "" {
				return v
			}
		}
	}
	return "verified"
}

func buildGraphExplain(graph graphschema.Graph) map[string]any {
	nodeExplain := make([]map[string]any, 0, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeExplain = append(nodeExplain, map[string]any{
			"id":                  n.ID,
			"type":                n.Type,
			"label":               n.Label,
			"service_id":          n.ServiceID,
			"section":             n.Section,
			"class":               n.Class,
			"verification_state":  n.VerificationState,
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
			"id":                 e.ID,
			"type":               e.Type,
			"source_id":          e.SourceID,
			"target_id":          e.TargetID,
			"section":            e.Section,
			"class":              e.Class,
			"verification_state": e.VerificationState,
			"confidence":         e.Confidence,
			"inferred":           e.Inferred,
			"attributes":         e.Attributes,
			"evidence_refs":      e.EvidenceRefs,
		})
	}
	return map[string]any{
		"meta":  graph.Meta,
		"nodes": nodeExplain,
		"edges": edgeExplain,
	}
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func equalsFoldTrimmed(a string, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func attrString(attrs map[string]any, key string) string {
	if attrs == nil {
		return ""
	}
	for k, v := range attrs {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(key)) {
			return strings.TrimSpace(fmt.Sprint(v))
		}
	}
	return ""
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

func buildGraphSummary(graph graphschema.Graph) map[string]any {
	serviceCount := 0
	for _, n := range graph.Nodes {
		if n.Type == "service" {
			serviceCount++
		}
	}
	density := 0.0
	nodeCount := len(graph.Nodes)
	if nodeCount > 1 {
		maxEdges := float64(nodeCount * (nodeCount - 1))
		density = float64(len(graph.Edges)) / maxEdges
	}
	return map[string]any{
		"graph_id":      graph.GraphID,
		"mode":          graph.Mode,
		"generated_at":  graph.GeneratedAt,
		"tenant_id":     graph.Meta.TenantID,
		"node_count":    len(graph.Nodes),
		"edge_count":    len(graph.Edges),
		"service_count": serviceCount,
		"density":       density,
		"by_node_type":  graph.Stats.ByNode,
		"by_edge_type":  graph.Stats.ByEdge,
		"freshness":     graph.Meta.Freshness,
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

func loadRuntimeReconcileIndex(graphRoot string) (runtimeReconcileIndex, error) {
	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}, nil
		}
		return runtimeReconcileIndex{}, err
	}
	index := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if err := json.Unmarshal(data, &index); err != nil {
		return runtimeReconcileIndex{}, fmt.Errorf("decode runtime reconcile index: %w", err)
	}
	return index, nil
}

func filterRuntimeReconcileRunsByTenant(r *http.Request, runs []runtimeReconcileSummary) []runtimeReconcileSummary {
	authCtx, err := security.ContextFromHeaders(r.Header)
	if err != nil || authCtx.HasRole("platform_admin") {
		return runs
	}
	tenantID := normalizeTenant(authCtx.TenantID)
	filtered := make([]runtimeReconcileSummary, 0, len(runs))
	for _, run := range runs {
		if normalizeTenant(run.TenantID) == tenantID {
			filtered = append(filtered, run)
		}
	}
	return filtered
}

func listRuntimeReconcileHistory(graphRoot string, r *http.Request, w http.ResponseWriter) {
	index, err := loadRuntimeReconcileIndex(graphRoot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	index.Runs = filterRuntimeReconcileRunsByTenant(r, index.Runs)
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
		for i, item := range index.Runs {
			if item.ReconcileID == before {
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
	if start > len(index.Runs) {
		start = len(index.Runs)
	}
	runs := index.Runs[start:]
	nextBefore := ""
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
		nextBefore = runs[len(runs)-1].ReconcileID
	}
	writeJSON(w, http.StatusOK, runtimeReconcileIndex{Runs: runs, NextBefore: nextBefore})
}

func persistRuntimeReconcileResult(graphRoot string, tenantID string, req contracts.RuntimeReconciliationRequest, result contracts.RuntimeReconciliationResult) (string, time.Time, error) {
	now := time.Now().UTC()
	recID := fmt.Sprintf("%d", now.UnixNano())
	baseDir := filepath.Join(graphRoot, "runtime", "reconcile", recID)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", time.Time{}, fmt.Errorf("create runtime reconcile dir: %w", err)
	}
	payload := runtimeReconcileRecord{
		ReconcileID: recID,
		GeneratedAt: now,
		TenantID:    normalizeTenant(tenantID),
		Request:     req,
		Result:      result,
	}
	outPath := filepath.Join(baseDir, "result.json")
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal runtime reconcile result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "", time.Time{}, fmt.Errorf("write runtime reconcile result: %w", err)
	}

	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	idx := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if indexData, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(indexData, &idx)
	}
	idx.Runs = append(idx.Runs, runtimeReconcileSummary{
		ReconcileID:  recID,
		GeneratedAt:  now,
		TenantID:     normalizeTenant(tenantID),
		GraphID:      strings.TrimSpace(req.GraphID),
		Claims:       len(req.Claims),
		Observations: len(req.Observations),
		Confirmed:    len(result.Confirmed),
		Contradicted: len(result.Contradicted),
		Unmapped:     len(result.Unmapped),
		NeedsReview:  len(result.NeedsReview),
		Path:         outPath,
	})
	sort.Slice(idx.Runs, func(i, j int) bool {
		return idx.Runs[i].GeneratedAt.After(idx.Runs[j].GeneratedAt)
	})
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal runtime reconcile index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return "", time.Time{}, fmt.Errorf("create runtime reconcile index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return "", time.Time{}, fmt.Errorf("write runtime reconcile index: %w", err)
	}
	return recID, now, nil
}

func loadRuntimeReconcileResult(graphRoot string, reconcileID string) (runtimeReconcileRecord, error) {
	path := filepath.Join(graphRoot, "runtime", "reconcile", reconcileID, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimeReconcileRecord{}, err
	}
	var payload runtimeReconcileRecord
	if err := json.Unmarshal(data, &payload); err != nil {
		return runtimeReconcileRecord{}, fmt.Errorf("decode runtime reconcile result: %w", err)
	}
	return payload, nil
}

func compareRuntimeReconcileRuns(from runtimeReconcileRecord, to runtimeReconcileRecord) runtimeReconcileCompare {
	confirmedAdded, confirmedRemoved := diffStringSets(from.Result.Confirmed, to.Result.Confirmed)
	contradictedAdded, contradictedRemoved := diffStringSets(from.Result.Contradicted, to.Result.Contradicted)
	unmappedAdded, unmappedRemoved := diffStringSets(from.Result.Unmapped, to.Result.Unmapped)
	needsReviewAdded, needsReviewRemoved := diffStringSets(from.Result.NeedsReview, to.Result.NeedsReview)
	return runtimeReconcileCompare{
		FromReconcileID:     from.ReconcileID,
		ToReconcileID:       to.ReconcileID,
		FromGraphID:         from.Result.GraphID,
		ToGraphID:           to.Result.GraphID,
		ConfirmedAdded:      confirmedAdded,
		ConfirmedRemoved:    confirmedRemoved,
		ContradictedAdded:   contradictedAdded,
		ContradictedRemoved: contradictedRemoved,
		UnmappedAdded:       unmappedAdded,
		UnmappedRemoved:     unmappedRemoved,
		NeedsReviewAdded:    needsReviewAdded,
		NeedsReviewRemoved:  needsReviewRemoved,
	}
}

func diffStringSets(from []string, to []string) ([]string, []string) {
	fromSet := map[string]struct{}{}
	toSet := map[string]struct{}{}
	for _, v := range from {
		v = strings.TrimSpace(v)
		if v != "" {
			fromSet[v] = struct{}{}
		}
	}
	for _, v := range to {
		v = strings.TrimSpace(v)
		if v != "" {
			toSet[v] = struct{}{}
		}
	}
	added := make([]string, 0)
	removed := make([]string, 0)
	for v := range toSet {
		if _, ok := fromSet[v]; !ok {
			added = append(added, v)
		}
	}
	for v := range fromSet {
		if _, ok := toSet[v]; !ok {
			removed = append(removed, v)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func deleteRuntimeReconcileResult(graphRoot string, reconcileID string) error {
	resultPath := filepath.Join(graphRoot, "runtime", "reconcile", reconcileID, "result.json")
	if _, err := os.Stat(resultPath); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Dir(resultPath)); err != nil {
		return fmt.Errorf("remove runtime reconcile dir: %w", err)
	}
	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	idx := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &idx)
	}
	filtered := make([]runtimeReconcileSummary, 0, len(idx.Runs))
	for _, item := range idx.Runs {
		if item.ReconcileID != reconcileID {
			filtered = append(filtered, item)
		}
	}
	idx.Runs = filtered
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime reconcile index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return fmt.Errorf("write runtime reconcile index: %w", err)
	}
	return nil
}

func pruneRuntimeReconcileHistory(graphRoot string, keepLatest int, tenantScope string) (int, error) {
	indexPath := filepath.Join(graphRoot, "runtime", "reconcile", "index.json")
	idx := runtimeReconcileIndex{Runs: []runtimeReconcileSummary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		if err := json.Unmarshal(data, &idx); err != nil {
			return 0, fmt.Errorf("decode runtime reconcile index: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, err
	}
	tenantScope = strings.TrimSpace(tenantScope)
	tenantScopeNorm := ""
	if tenantScope != "" {
		tenantScopeNorm = normalizeTenant(tenantScope)
	}
	var target []runtimeReconcileSummary
	var passthrough []runtimeReconcileSummary
	if tenantScopeNorm == "" {
		target = idx.Runs
	} else {
		target = make([]runtimeReconcileSummary, 0, len(idx.Runs))
		passthrough = make([]runtimeReconcileSummary, 0, len(idx.Runs))
		for _, item := range idx.Runs {
			if normalizeTenant(item.TenantID) == tenantScopeNorm {
				target = append(target, item)
			} else {
				passthrough = append(passthrough, item)
			}
		}
	}
	if keepLatest >= len(target) {
		return 0, nil
	}
	toDelete := target[keepLatest:]
	deleted := 0
	for _, item := range toDelete {
		if item.Path == "" {
			continue
		}
		if err := os.RemoveAll(filepath.Dir(item.Path)); err == nil {
			deleted++
		}
	}
	if tenantScopeNorm == "" {
		idx.Runs = target[:keepLatest]
	} else {
		idx.Runs = append(passthrough, target[:keepLatest]...)
		sort.Slice(idx.Runs, func(i, j int) bool {
			return idx.Runs[i].GeneratedAt.After(idx.Runs[j].GeneratedAt)
		})
	}
	indexData, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return deleted, fmt.Errorf("marshal runtime reconcile index: %w", err)
	}
	indexData = append(indexData, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return deleted, fmt.Errorf("create runtime reconcile index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, indexData, 0o644); err != nil {
		return deleted, fmt.Errorf("write runtime reconcile index: %w", err)
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

func defaultTemplateCatalogPath() string {
	return filepath.Join("docs", "m15_query_templates.json")
}

func defaultQuestionCatalogPath() string {
	return filepath.Join("docs", "m17_question_catalog.json")
}

func resolveReadablePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return path
	}
	candidates := []string{
		path,
		filepath.Join("..", path),
		filepath.Join("..", "..", path),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return path
}

func loadProductTemplates(path string) (productTemplateFile, error) {
	path = resolveReadablePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return productTemplateFile{}, err
	}
	var payload productTemplateFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return productTemplateFile{}, fmt.Errorf("decode product templates: %w", err)
	}
	return payload, nil
}

func loadProductQuestions(path string) (productQuestionFile, error) {
	path = resolveReadablePath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return productQuestionFile{}, err
	}
	var payload productQuestionFile
	if err := json.Unmarshal(data, &payload); err != nil {
		return productQuestionFile{}, fmt.Errorf("decode product question catalog: %w", err)
	}
	return payload, nil
}

func findTemplateByID(catalog productTemplateFile, templateID string) *productTemplate {
	for i := range catalog.Templates {
		if strings.TrimSpace(catalog.Templates[i].ID) == templateID {
			return &catalog.Templates[i]
		}
	}
	return nil
}

func findTemplateByEndpoint(catalog productTemplateFile, endpoint string) *productTemplate {
	needle := normalizeTemplatePath(strings.TrimSpace(endpoint))
	for i := range catalog.Templates {
		path := normalizeTemplatePath(strings.TrimSpace(catalog.Templates[i].Path))
		if path == "" {
			continue
		}
		if matchCatalogPath(path, needle) {
			return &catalog.Templates[i]
		}
	}
	return nil
}

func executeProductTemplate(graphRoot string, authHeaders http.Header, templateID string, templatePath string, vars map[string]any) (int, map[string]any) {
	resolvedTemplatePath := resolveReadablePath(templatePath)
	catalog, err := loadProductTemplates(resolvedTemplatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return http.StatusNotFound, map[string]any{"error": "product templates not found"}
		}
		return http.StatusInternalServerError, map[string]any{"error": err.Error()}
	}
	tmpl := findTemplateByID(catalog, templateID)
	if tmpl == nil {
		return http.StatusNotFound, map[string]any{"error": "template not found"}
	}
	method := strings.ToUpper(strings.TrimSpace(tmpl.Method))
	if method == "" {
		method = http.MethodGet
	}
	targetPath := interpolateTemplateString(strings.TrimSpace(tmpl.Path), vars)
	if targetPath == "" {
		return http.StatusBadRequest, map[string]any{"error": "template path resolves to empty"}
	}
	if !strings.HasPrefix(targetPath, "/products/") || strings.HasPrefix(targetPath, "/products/templates") || strings.HasPrefix(targetPath, "/products/questions") {
		return http.StatusBadRequest, map[string]any{"error": "template path is not executable"}
	}
	queryVals := url.Values{}
	for k, v := range tmpl.Query {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		value := interpolateTemplateAny(v, vars)
		if value == nil {
			continue
		}
		queryVals.Set(key, fmt.Sprint(value))
	}
	resolvedPath := targetPath
	if encoded := queryVals.Encode(); encoded != "" {
		sep := "?"
		if strings.Contains(resolvedPath, "?") {
			sep = "&"
		}
		resolvedPath += sep + encoded
	}
	bodyPayload := interpolateTemplateAny(tmpl.Payload, vars)
	var bodyReader io.Reader
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
		if bodyPayload == nil {
			bodyPayload = map[string]any{}
		}
		b, err := json.Marshal(bodyPayload)
		if err != nil {
			return http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("encode resolved payload: %v", err)}
		}
		bodyReader = bytes.NewReader(b)
	}
	execReq := httptest.NewRequest(method, resolvedPath, bodyReader)
	for k, values := range authHeaders {
		if len(values) == 0 {
			continue
		}
		execReq.Header[k] = append([]string(nil), values...)
	}
	if bodyReader != nil {
		execReq.Header.Set("Content-Type", "application/json")
	}
	var h http.Handler
	switch {
	case strings.HasPrefix(targetPath, "/products/pr-review"):
		h = handleProductPRReview(graphRoot)
	case strings.HasPrefix(targetPath, "/products/docs/"):
		h = handleProductDocs(graphRoot)
	case strings.HasPrefix(targetPath, "/products/mapper/"):
		h = handleProductMapper(graphRoot)
	case strings.HasPrefix(targetPath, "/products/governance/"):
		h = handleProductGovernance(graphRoot)
	default:
		return http.StatusBadRequest, map[string]any{"error": "template path is not mapped to a product handler"}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, execReq)
	var result any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		result = map[string]any{"raw": rec.Body.String()}
	}
	resp := map[string]any{
		"template_id":   templateID,
		"template_path": resolvedTemplatePath,
		"method":        method,
		"path":          targetPath,
		"query":         queryVals,
		"status":        rec.Code,
		"result":        result,
	}
	if bodyPayload != nil {
		resp["payload"] = bodyPayload
	}
	if rec.Code >= 400 {
		resp["error"] = result
	}
	return rec.Code, resp
}

func handleProductTemplates(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("path"))
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedPath := resolveReadablePath(templatePath)
		payload, err := loadProductTemplates(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":      resolvedPath,
			"count":     len(payload.Templates),
			"templates": payload.Templates,
		})
	}
}

func handleProductTemplateExecute(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := productTemplateExecuteRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.TemplateID = strings.TrimSpace(req.TemplateID)
		if req.TemplateID == "" {
			writeError(w, http.StatusBadRequest, "template_id is required")
			return
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		status, resp := executeProductTemplate(graphRoot, r.Header, req.TemplateID, templatePath, req.Vars)
		writeJSON(w, status, resp)
	}
}

func handleProductQuestions(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		catalogPath := strings.TrimSpace(r.URL.Query().Get("catalog_path"))
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		resolvedPath := resolveReadablePath(catalogPath)
		payload, err := loadProductQuestions(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":      resolvedPath,
			"count":     len(payload.Questions),
			"questions": payload.Questions,
		})
	}
}

func handleProductQuestionExecute(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := productQuestionExecuteRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		req.QuestionID = strings.TrimSpace(req.QuestionID)
		if req.QuestionID == "" {
			writeError(w, http.StatusBadRequest, "question_id is required")
			return
		}
		catalogPath := strings.TrimSpace(req.CatalogPath)
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)
		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var selectedEndpoint string
		for _, q := range questions.Questions {
			if strings.TrimSpace(q.ID) == req.QuestionID {
				selectedEndpoint = strings.TrimSpace(q.Endpoint)
				break
			}
		}
		if selectedEndpoint == "" {
			writeError(w, http.StatusNotFound, "question not found")
			return
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		templates, err := loadProductTemplates(templatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		template := findTemplateByEndpoint(templates, selectedEndpoint)
		if template == nil {
			writeError(w, http.StatusNotFound, "no template mapped to question endpoint")
			return
		}
		status, resp := executeProductTemplate(graphRoot, r.Header, strings.TrimSpace(template.ID), templatePath, req.Vars)
		resp["question_id"] = req.QuestionID
		resp["question_endpoint"] = selectedEndpoint
		resp["catalog_path"] = resolvedCatalogPath
		writeJSON(w, status, resp)
	}
}

func handleProductQuestionRun(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		req := productQuestionRunRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		catalogPath := strings.TrimSpace(req.CatalogPath)
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		templatePath := strings.TrimSpace(req.TemplatePath)
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)
		resolvedTemplatePath := resolveReadablePath(templatePath)

		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		templates, err := loadProductTemplates(resolvedTemplatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		selected := map[string]struct{}{}
		if len(req.QuestionIDs) > 0 {
			for _, id := range req.QuestionIDs {
				id = strings.TrimSpace(id)
				if id != "" {
					selected[id] = struct{}{}
				}
			}
		}

		results := make([]map[string]any, 0, len(questions.Questions))
		success := 0
		failed := 0
		for _, q := range questions.Questions {
			qid := strings.TrimSpace(q.ID)
			if len(selected) > 0 {
				if _, ok := selected[qid]; !ok {
					continue
				}
			}
			endpoint := strings.TrimSpace(q.Endpoint)
			mapped := findTemplateByEndpoint(templates, endpoint)
			item := map[string]any{
				"question_id":       qid,
				"question":          strings.TrimSpace(q.Question),
				"question_endpoint": endpoint,
			}
			if mapped == nil {
				failed++
				item["status"] = http.StatusNotFound
				item["error"] = "no template mapped to question endpoint"
				results = append(results, item)
				continue
			}
			item["template_id"] = strings.TrimSpace(mapped.ID)
			vars := map[string]any{}
			for k, v := range req.Vars {
				vars[k] = v
			}
			if qVars, ok := req.VarsByQuestion[qid]; ok {
				for k, v := range qVars {
					vars[k] = v
				}
			}
			status, resp := executeProductTemplate(graphRoot, r.Header, strings.TrimSpace(mapped.ID), resolvedTemplatePath, vars)
			item["status"] = status
			item["response"] = resp
			if status >= 200 && status < 300 {
				success++
			} else {
				failed++
			}
			results = append(results, item)
		}
		total := len(results)
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_path":   resolvedCatalogPath,
			"template_path":  resolvedTemplatePath,
			"total":          total,
			"succeeded":      success,
			"failed":         failed,
			"overall_passed": failed == 0 && total > 0,
			"results":        results,
		})
	}
}

func handleProductQuestionCoverage(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionQueryGraph, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		catalogPath := strings.TrimSpace(r.URL.Query().Get("catalog_path"))
		if catalogPath == "" {
			catalogPath = defaultQuestionCatalogPath()
		}
		templatePath := strings.TrimSpace(r.URL.Query().Get("template_path"))
		if templatePath == "" {
			templatePath = defaultTemplateCatalogPath()
		}
		resolvedCatalogPath := resolveReadablePath(catalogPath)
		resolvedTemplatePath := resolveReadablePath(templatePath)
		questions, err := loadProductQuestions(resolvedCatalogPath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product question catalog not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		templates, err := loadProductTemplates(resolvedTemplatePath)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "product templates not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		type item struct {
			QuestionID       string `json:"question_id"`
			Question         string `json:"question"`
			Endpoint         string `json:"endpoint"`
			Covered          bool   `json:"covered"`
			MappedTemplateID string `json:"mapped_template_id,omitempty"`
		}
		items := make([]item, 0, len(questions.Questions))
		coveredCount := 0
		for _, q := range questions.Questions {
			endpoint := strings.TrimSpace(q.Endpoint)
			mapped := findTemplateByEndpoint(templates, endpoint)
			entry := item{
				QuestionID: strings.TrimSpace(q.ID),
				Question:   strings.TrimSpace(q.Question),
				Endpoint:   endpoint,
			}
			if mapped != nil {
				entry.Covered = true
				entry.MappedTemplateID = strings.TrimSpace(mapped.ID)
				coveredCount++
			}
			items = append(items, entry)
		}
		coverage := 1.0
		if len(items) > 0 {
			coverage = float64(coveredCount) / float64(len(items))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"catalog_path":   resolvedCatalogPath,
			"template_path":  resolvedTemplatePath,
			"total":          len(items),
			"covered":        coveredCount,
			"coverage_ratio": coverage,
			"items":          items,
		})
	}
}

func interpolateTemplateString(input string, vars map[string]any) string {
	out := input
	for key, value := range vars {
		token := "${" + strings.TrimSpace(key) + "}"
		out = strings.ReplaceAll(out, token, fmt.Sprint(value))
	}
	return out
}

func interpolateTemplateAny(value any, vars map[string]any) any {
	switch v := value.(type) {
	case string:
		return interpolateTemplateString(v, vars)
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, interpolateTemplateAny(item, vars))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = interpolateTemplateAny(item, vars)
		}
		return out
	default:
		return value
	}
}

func normalizeTemplatePath(path string) string {
	if path == "" {
		return ""
	}
	out := strings.TrimSpace(path)
	if idx := strings.Index(out, "?"); idx >= 0 {
		out = out[:idx]
	}
	out = strings.ReplaceAll(out, "{graph_id}", "${graph_id}")
	out = strings.ReplaceAll(out, "{service_id}", "${service_id}")
	out = strings.ReplaceAll(out, "$${graph_id}", "${graph_id}")
	out = strings.ReplaceAll(out, "$${service_id}", "${service_id}")
	out = strings.TrimRight(out, "/")
	if out == "" {
		return "/"
	}
	return out
}

func splitPathParts(path string) []string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return nil
	}
	raw := strings.Split(path, "/")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isPathPlaceholder(part string) bool {
	part = strings.TrimSpace(part)
	if part == "" {
		return false
	}
	return strings.HasPrefix(part, "${") && strings.HasSuffix(part, "}") ||
		strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")
}

func matchCatalogPath(templatePath string, catalogPath string) bool {
	templateParts := splitPathParts(templatePath)
	catalogParts := splitPathParts(catalogPath)
	if len(templateParts) != len(catalogParts) {
		return false
	}
	for i := range templateParts {
		a := templateParts[i]
		b := catalogParts[i]
		if isPathPlaceholder(a) || isPathPlaceholder(b) {
			continue
		}
		if a != b {
			return false
		}
	}
	return true
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
		runtimeSLO := map[string]any{
			"total_runs":                   0,
			"decision_total":               0,
			"confirmed":                    0,
			"contradicted":                 0,
			"needs_review":                 0,
			"runtime_only_unmapped":        0,
			"confirmed_rate":               0.0,
			"contradicted_rate":            0.0,
			"needs_review_rate":            0.0,
			"runtime_only_unmapped_rate":   0.0,
			"target_confirmed_rate":        0.9,
			"runtime_quality_passed":       true,
			"runtime_quality_reason":       "no_runtime_reconciliation_runs",
			"runtime_quality_gate_enabled": true,
		}
		runtimePassed := true
		if authCtx, err := security.ContextFromHeaders(r.Header); err == nil {
			if index, err := loadRuntimeReconcileIndex(graphRoot); err == nil {
				runs := index.Runs
				if !authCtx.HasRole("platform_admin") {
					runs = filterRuntimeReconcileRunsByTenant(r, runs)
				}
				totalRuns := len(runs)
				totalConfirmed := 0
				totalContradicted := 0
				totalNeedsReview := 0
				totalUnmapped := 0
				for _, run := range runs {
					totalConfirmed += run.Confirmed
					totalContradicted += run.Contradicted
					totalNeedsReview += run.NeedsReview
					totalUnmapped += run.Unmapped
				}
				decisionTotal := totalConfirmed + totalContradicted + totalNeedsReview + totalUnmapped
				confirmedRate := 0.0
				contradictedRate := 0.0
				needsReviewRate := 0.0
				unmappedRate := 0.0
				if decisionTotal > 0 {
					confirmedRate = float64(totalConfirmed) / float64(decisionTotal)
					contradictedRate = float64(totalContradicted) / float64(decisionTotal)
					needsReviewRate = float64(totalNeedsReview) / float64(decisionTotal)
					unmappedRate = float64(totalUnmapped) / float64(decisionTotal)
				}
				runtimePassed = totalRuns == 0 || confirmedRate >= 0.9
				reason := "no_runtime_reconciliation_runs"
				if totalRuns > 0 {
					if runtimePassed {
						reason = "confirmed_rate_meets_target"
					} else {
						reason = "confirmed_rate_below_target"
					}
				}
				runtimeSLO = map[string]any{
					"total_runs":                   totalRuns,
					"decision_total":               decisionTotal,
					"confirmed":                    totalConfirmed,
					"contradicted":                 totalContradicted,
					"needs_review":                 totalNeedsReview,
					"runtime_only_unmapped":        totalUnmapped,
					"confirmed_rate":               confirmedRate,
					"contradicted_rate":            contradictedRate,
					"needs_review_rate":            needsReviewRate,
					"runtime_only_unmapped_rate":   unmappedRate,
					"target_confirmed_rate":        0.9,
					"runtime_quality_passed":       runtimePassed,
					"runtime_quality_reason":       reason,
					"runtime_quality_gate_enabled": true,
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"generated_at":            time.Now().UTC(),
			"critical_requests":       total,
			"critical_successes":      success,
			"slo_adherence":           adherence,
			"slo_target":              99.9,
			"slo_passed":              adherence >= 99.9 && runtimePassed,
			"critical_latency_p95_ms": p95,
			"slo_checks": map[string]any{
				"api_availability_passed": adherence >= 99.9,
				"runtime_quality_passed":  runtimePassed,
			},
			"runtime_reconciliation": runtimeSLO,
			"routes":                 routes,
		})
	}
}

func finalArtifactsRoot(graphRoot string) string {
	root := strings.TrimSpace(graphRoot)
	if root == "" {
		return ".diffmind"
	}
	if strings.EqualFold(filepath.Base(root), "graph") {
		return filepath.Dir(root)
	}
	return root
}

func defaultFinalGatePaths(graphRoot string) (string, string, string, string, string, string, string) {
	root := finalArtifactsRoot(graphRoot)
	return filepath.Join(root, "quality", "gate_result.json"),
		filepath.Join(root, "ops", "slo_report.json"),
		filepath.Join("docs", "m15_query_templates.json"),
		filepath.Join("docs", "m17_question_catalog.json"),
		filepath.Join(root, "graph", "index.json"),
		filepath.Join(root, "final", "readiness_report.json"),
		filepath.Join(root, "final", "gate_decision.md")
}

func handleFinalGateAttest(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditExport, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, true, graphRoot); !ok {
			return
		}
		qualityPath, sloPath, templatesPath, catalogPath, graphIndexPath, outReportPath, outDecisionPath := defaultFinalGatePaths(graphRoot)
		req := finalGateAttestRequest{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
			return
		}
		if v := strings.TrimSpace(req.QualityGatePath); v != "" {
			qualityPath = v
		}
		if v := strings.TrimSpace(req.SLOPath); v != "" {
			sloPath = v
		}
		if v := strings.TrimSpace(req.TemplatesPath); v != "" {
			templatesPath = v
		}
		if v := strings.TrimSpace(req.CatalogPath); v != "" {
			catalogPath = v
		}
		if v := strings.TrimSpace(req.GraphIndexPath); v != "" {
			graphIndexPath = v
		}
		if v := strings.TrimSpace(req.OutReportPath); v != "" {
			outReportPath = v
		}
		if v := strings.TrimSpace(req.OutDecisionPath); v != "" {
			outDecisionPath = v
		}
		args := []string{
			"attest",
			"--quality-gate", qualityPath,
			"--slo", sloPath,
			"--templates", templatesPath,
			"--catalog", catalogPath,
			"--graph-index", graphIndexPath,
			"--out-report", outReportPath,
			"--out-decision", outDecisionPath,
		}
		if len(req.Signers) > 0 {
			signers := make([]string, 0, len(req.Signers))
			for _, s := range req.Signers {
				s = strings.TrimSpace(s)
				if s != "" {
					signers = append(signers, s)
				}
			}
			if len(signers) > 0 {
				args = append(args, "--signers", strings.Join(signers, ","))
			}
		}
		runErr := finalgate.Run(r.Context(), args)
		resp := map[string]any{
			"quality_gate_path": qualityPath,
			"slo_path":          sloPath,
			"templates_path":    templatesPath,
			"catalog_path":      catalogPath,
			"graph_index_path":  graphIndexPath,
			"report_path":       outReportPath,
			"decision_path":     outDecisionPath,
		}
		if reportData, err := os.ReadFile(outReportPath); err == nil {
			var reportPayload any
			if json.Unmarshal(reportData, &reportPayload) == nil {
				resp["readiness_report"] = reportPayload
			}
		}
		if decisionData, err := os.ReadFile(outDecisionPath); err == nil {
			resp["gate_decision_markdown"] = string(decisionData)
		}
		if runErr != nil {
			resp["attest_error"] = runErr.Error()
			resp["overall_passed"] = false
			if _, ok := resp["readiness_report"]; ok {
				writeJSON(w, http.StatusOK, resp)
				return
			}
			writeError(w, http.StatusInternalServerError, runErr.Error())
			return
		}
		resp["overall_passed"] = true
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleFinalReadiness(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, _, _, _, defaultPath, _ := defaultFinalGatePaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "final readiness report not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var payload any
		if err := json.Unmarshal(data, &payload); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("decode readiness report: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":   path,
			"report": payload,
		})
	}
}

func handleFinalDecision(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionAuditRead, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), true, false, graphRoot); !ok {
			return
		}
		_, _, _, _, _, _, defaultPath := defaultFinalGatePaths(graphRoot)
		path := strings.TrimSpace(r.URL.Query().Get("path"))
		if path == "" {
			path = defaultPath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "final gate decision not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    path,
			"content": string(data),
		})
	}
}

func handleRuntimePlan(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		writeJSON(w, http.StatusOK, runtimepkg.DefaultPlan())
	}
}

func parseRuntimeReportTimeParam(raw string, name string) (time.Time, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false, nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("invalid %s (expected RFC3339)", name)
	}
	return ts.UTC(), true, nil
}

func handleRuntimeReconcileReport(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		from, hasFrom, err := parseRuntimeReportTimeParam(r.URL.Query().Get("from"), "from")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		to, hasTo, err := parseRuntimeReportTimeParam(r.URL.Query().Get("to"), "to")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if hasFrom && hasTo && from.After(to) {
			writeError(w, http.StatusBadRequest, "from must be <= to")
			return
		}
		graphFilter := strings.TrimSpace(r.URL.Query().Get("graph_id"))

		index, err := loadRuntimeReconcileIndex(graphRoot)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		filtered := filterRuntimeReconcileRunsByTenant(r, index.Runs)
		if graphFilter != "" {
			next := make([]runtimeReconcileSummary, 0, len(filtered))
			for _, run := range filtered {
				if strings.TrimSpace(run.GraphID) == graphFilter {
					next = append(next, run)
				}
			}
			filtered = next
		}
		if hasFrom || hasTo {
			next := make([]runtimeReconcileSummary, 0, len(filtered))
			for _, run := range filtered {
				ts := run.GeneratedAt.UTC()
				if hasFrom && ts.Before(from) {
					continue
				}
				if hasTo && ts.After(to) {
					continue
				}
				next = append(next, run)
			}
			filtered = next
		}

		authCtx, authErr := security.ContextFromHeaders(r.Header)
		report := runtimeReconcileReport{
			GeneratedAt: time.Now().UTC(),
			GraphID:     graphFilter,
			TopGraphs:   []runtimeReconcileGraphSummary{},
		}
		if authErr == nil && !authCtx.HasRole("platform_admin") {
			report.TenantID = normalizeTenant(authCtx.TenantID)
		}
		if hasFrom {
			report.From = from.Format(time.RFC3339)
		}
		if hasTo {
			report.To = to.Format(time.RFC3339)
		}
		report.TotalRuns = len(filtered)
		if len(filtered) > 0 {
			latest := filtered[0]
			report.LatestRun = &latest
		}
		graphs := map[string]*runtimeReconcileGraphSummary{}
		for _, run := range filtered {
			report.TotalClaims += run.Claims
			report.TotalObservations += run.Observations
			report.TotalConfirmed += run.Confirmed
			report.TotalContradicted += run.Contradicted
			report.TotalUnmapped += run.Unmapped
			report.TotalNeedsReview += run.NeedsReview

			stat, ok := graphs[run.GraphID]
			if !ok {
				stat = &runtimeReconcileGraphSummary{GraphID: run.GraphID}
				graphs[run.GraphID] = stat
			}
			stat.Runs++
			stat.Claims += run.Claims
			stat.Observations += run.Observations
			stat.Confirmed += run.Confirmed
			stat.Contradicted += run.Contradicted
			stat.Unmapped += run.Unmapped
			stat.NeedsReview += run.NeedsReview
			if run.GeneratedAt.After(stat.LastRunAt) {
				stat.LastRunAt = run.GeneratedAt
			}
		}
		decisionTotal := report.TotalConfirmed + report.TotalContradicted + report.TotalNeedsReview + report.TotalUnmapped
		if decisionTotal > 0 {
			report.ConfirmedRate = float64(report.TotalConfirmed) / float64(decisionTotal)
			report.ContradictedRate = float64(report.TotalContradicted) / float64(decisionTotal)
			report.NeedsReviewRate = float64(report.TotalNeedsReview) / float64(decisionTotal)
			report.UnmappedRate = float64(report.TotalUnmapped) / float64(decisionTotal)
		}
		report.TopGraphs = make([]runtimeReconcileGraphSummary, 0, len(graphs))
		for _, stat := range graphs {
			report.TopGraphs = append(report.TopGraphs, *stat)
		}
		sort.Slice(report.TopGraphs, func(i, j int) bool {
			if report.TopGraphs[i].Runs == report.TopGraphs[j].Runs {
				return report.TopGraphs[i].GraphID < report.TopGraphs[j].GraphID
			}
			return report.TopGraphs[i].Runs > report.TopGraphs[j].Runs
		})
		if len(report.TopGraphs) > 5 {
			report.TopGraphs = report.TopGraphs[:5]
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func handleRuntimeReconcile(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			listRuntimeReconcileHistory(graphRoot, r, w)
			return
		case http.MethodPost:
			req := contracts.RuntimeReconciliationRequest{}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("decode request body: %v", err))
				return
			}
			if strings.TrimSpace(req.GraphID) == "" {
				writeError(w, http.StatusBadRequest, "graph_id is required")
				return
			}
			resourceTenant := normalizeTenant(req.TenantID)
			if req.TenantID == "" {
				resourceTenant = normalizeTenant(r.Header.Get("X-DiffMind-Tenant"))
			}
			if _, ok := authorizeRequest(w, r, security.ActionRuntimeRun, resourceTenant, false, true, graphRoot); !ok {
				return
			}
			result, err := runtimepkg.Reconcile(r.Context(), req)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			recID, generatedAt, err := persistRuntimeReconcileResult(graphRoot, normalizeTenant(resourceTenant), req, result)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"reconcile_id":          recID,
				"generated_at":          generatedAt,
				"tenant_id":             normalizeTenant(resourceTenant),
				"graph_id":              result.GraphID,
				"confirmed":             result.Confirmed,
				"contradicted":          result.Contradicted,
				"runtime_only_unmapped": result.Unmapped,
				"needs_review":          result.NeedsReview,
			})
			return
		case http.MethodDelete:
			authCtx, ok := authorizeRequest(w, r, security.ActionRuntimeRun, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			keepLatest := 20
			if q := strings.TrimSpace(r.URL.Query().Get("keep_latest")); q != "" {
				n, err := strconv.Atoi(q)
				if err != nil || n < 0 {
					writeError(w, http.StatusBadRequest, "invalid keep_latest")
					return
				}
				keepLatest = n
			}
			scopeTenant := ""
			if !authCtx.HasRole("platform_admin") {
				scopeTenant = normalizeTenant(authCtx.TenantID)
			}
			deleted, err := pruneRuntimeReconcileHistory(graphRoot, keepLatest, scopeTenant)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"deleted":     deleted,
				"keep_latest": keepLatest,
			})
			return
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleRuntimeReconcileByID(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recID := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/runtime/reconcile/"))
		recID = strings.Trim(recID, "/")
		if recID == "" {
			writeError(w, http.StatusBadRequest, "reconcile id is required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
				return
			}
			payload, err := loadRuntimeReconcileResult(graphRoot, recID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "runtime reconcile result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			authCtx, err := security.ContextFromHeaders(r.Header)
			if err != nil {
				writeError(w, http.StatusUnauthorized, err.Error())
				return
			}
			if !authCtx.HasRole("platform_admin") && normalizeTenant(payload.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
			writeJSON(w, http.StatusOK, payload)
			return
		case http.MethodDelete:
			authCtx, ok := authorizeRequest(w, r, security.ActionRuntimeRun, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, true, graphRoot)
			if !ok {
				return
			}
			payload, err := loadRuntimeReconcileResult(graphRoot, recID)
			if err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "runtime reconcile result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !authCtx.HasRole("platform_admin") && normalizeTenant(payload.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
			if err := deleteRuntimeReconcileResult(graphRoot, recID); err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "runtime reconcile result not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"deleted": recID})
			return
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}
}

func handleRuntimeReconcileCompare(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, normalizeTenant(r.Header.Get("X-DiffMind-Tenant")), false, false, graphRoot); !ok {
			return
		}
		fromID := strings.TrimSpace(r.URL.Query().Get("from"))
		toID := strings.TrimSpace(r.URL.Query().Get("to"))
		if fromID == "" || toID == "" {
			writeError(w, http.StatusBadRequest, "from and to query params are required")
			return
		}
		fromRec, err := loadRuntimeReconcileResult(graphRoot, fromID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "from runtime reconcile result not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		toRec, err := loadRuntimeReconcileResult(graphRoot, toID)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "to runtime reconcile result not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		authCtx, err := security.ContextFromHeaders(r.Header)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if !authCtx.HasRole("platform_admin") {
			if normalizeTenant(fromRec.TenantID) != normalizeTenant(authCtx.TenantID) || normalizeTenant(toRec.TenantID) != normalizeTenant(authCtx.TenantID) {
				writeError(w, http.StatusForbidden, "tenant_mismatch")
				return
			}
		}
		payload := compareRuntimeReconcileRuns(fromRec, toRec)
		writeJSON(w, http.StatusOK, payload)
	}
}

func handleRuntimeClaims(graphRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/runtime/claims/")
		graphID := strings.TrimSpace(strings.Trim(rest, "/"))
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
		if _, ok := authorizeRequest(w, r, security.ActionRuntimePlan, graphTenant(graph), false, false, graphRoot); !ok {
			return
		}

		policy := parsePublishPolicy(r)
		sections := runtimeClaimSections(r.URL.Query().Get("sections"))
		includeNodes := parseBoolDefault(r.URL.Query().Get("include_nodes"), true)
		includeEdges := parseBoolDefault(r.URL.Query().Get("include_edges"), true)
		claims := buildRuntimeClaims(graphID, graph, sections, includeNodes, includeEdges, policy)

		writeJSON(w, http.StatusOK, map[string]any{
			"graph_id": graphID,
			"count":    len(claims),
			"claims":   claims,
		})
	}
}

func runtimeClaimSections(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		out["exposure"] = struct{}{}
		out["dependencies"] = struct{}{}
		return out
	}
	for _, p := range strings.Split(raw, ",") {
		s := strings.ToLower(strings.TrimSpace(p))
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	if len(out) == 0 {
		out["exposure"] = struct{}{}
		out["dependencies"] = struct{}{}
	}
	return out
}

func buildRuntimeClaims(graphID string, graph graphschema.Graph, sections map[string]struct{}, includeNodes bool, includeEdges bool, policy publishPolicy) []contracts.RuntimeClaim {
	claims := make([]contracts.RuntimeClaim, 0)
	if includeNodes {
		for _, n := range graph.Nodes {
			sec := strings.ToLower(strings.TrimSpace(n.Section))
			if _, ok := sections[sec]; !ok {
				continue
			}
			if !shouldKeepByPublishPolicy(n.Section, n.VerificationState, n.Attributes, n.Inferred, policy) {
				continue
			}
			claims = append(claims, contracts.RuntimeClaim{
				GraphID: graphID,
				NodeID:  n.ID,
				Class:   n.Class,
				Section: n.Section,
				Metadata: map[string]string{
					"type":               n.Type,
					"label":              n.Label,
					"service_id":         n.ServiceID,
					"verification_state": effectiveVerificationState(n.VerificationState, n.Attributes, n.Inferred),
				},
			})
		}
	}
	if includeEdges {
		for _, e := range graph.Edges {
			sec := strings.ToLower(strings.TrimSpace(e.Section))
			if _, ok := sections[sec]; !ok {
				continue
			}
			if !shouldKeepByPublishPolicy(e.Section, e.VerificationState, e.Attributes, e.Inferred, policy) {
				continue
			}
			claims = append(claims, contracts.RuntimeClaim{
				GraphID:  graphID,
				EdgeID:   e.ID,
				Class:    e.Class,
				Section:  e.Section,
				Evidence: evidenceRefStrings(e.EvidenceRefs),
				Metadata: map[string]string{
					"type":               e.Type,
					"source_id":          e.SourceID,
					"target_id":          e.TargetID,
					"verification_state": effectiveVerificationState(e.VerificationState, e.Attributes, e.Inferred),
				},
			})
		}
	}
	sort.Slice(claims, func(i, j int) bool {
		ik := strings.TrimSpace(claims[i].EdgeID)
		if ik == "" {
			ik = strings.TrimSpace(claims[i].NodeID)
		}
		jk := strings.TrimSpace(claims[j].EdgeID)
		if jk == "" {
			jk = strings.TrimSpace(claims[j].NodeID)
		}
		return ik < jk
	})
	return claims
}

func evidenceRefStrings(refs []graphschema.EvidenceRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if path := strings.TrimSpace(ref.FilePath); path != "" {
			if ref.StartLine > 0 {
				out = append(out, fmt.Sprintf("%s:%d", path, ref.StartLine))
			} else {
				out = append(out, path)
			}
			continue
		}
		if id := strings.TrimSpace(ref.EvidenceID); id != "" {
			out = append(out, "evidence:"+id)
		}
	}
	return out
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
