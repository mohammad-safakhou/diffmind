package httpapi

import (
	"context"
	"embed"
	"encoding/json"
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
	"time"

	"diffmind/internal/bundleio"
	"diffmind/internal/diff"
	graphpkg "diffmind/internal/graph"
	"diffmind/internal/graphschema"
	"diffmind/internal/query"
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
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/defaults", handleDefaults(defaultBundlePath, graphRoot))
	mux.HandleFunc("/entities", handleEntities(defaultBundlePath))
	mux.HandleFunc("/diff", handleDiff)
	mux.HandleFunc("/graphs", handleGraphs(graphRoot))
	mux.HandleFunc("/graphs/build", handleGraphBuildAlias(graphRoot))
	mux.HandleFunc("/graphs/compare", handleGraphsCompare(graphRoot))
	mux.HandleFunc("/graphs/compare/", handleGraphsCompareByID(graphRoot))
	mux.HandleFunc("/graphs/", handleGraphByID(graphRoot))
	if sub, err := fs.Sub(uiFiles, "ui"); err == nil {
		mux.Handle("/", http.FileServer(http.FS(sub)))
	}
	return mux
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
			listCompareHistory(graphRoot, r, w)
			return
		case http.MethodDelete:
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
		if err := persistCompareResult(graphRoot, &result); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
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
			writeJSON(w, http.StatusOK, payload)
		case http.MethodDelete:
			if err := deleteCompareResult(graphRoot, compareID); err != nil {
				if os.IsNotExist(err) {
					writeError(w, http.StatusNotFound, "compare not found")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
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
	}
}

func handleDiff(w http.ResponseWriter, r *http.Request) {
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
			handleGraphsList(graphRoot, w)
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

func handleGraphsList(graphRoot string, w http.ResponseWriter) {
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
		Mode:               req.Mode,
		ServiceID:          req.ServiceID,
		ServiceName:        req.ServiceName,
		BundlePath:         req.BundlePath,
		AnalyzerBundlePath: req.AnalyzerBundlePath,
		BaseURLs:           req.BaseURLs,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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

		if len(parts) == 3 && parts[1] == "evidence" {
			edgeID := strings.TrimSpace(parts[2])
			if edgeID == "" {
				writeError(w, http.StatusBadRequest, "edge id is required")
				return
			}
			for _, e := range graph.Edges {
				if e.ID == edgeID {
					writeJSON(w, http.StatusOK, map[string]any{
						"graph_id":      graphID,
						"edge_id":       edgeID,
						"edge_type":     e.Type,
						"evidence_refs": e.EvidenceRefs,
					})
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
		if len(parts) == 2 && parts[1] == "metrics" {
			writeJSON(w, http.StatusOK, buildGraphMetrics(graph))
			return
		}
		writeJSON(w, http.StatusOK, graph)
	}
}

func loadGraph(graphRoot string, graphID string) (graphschema.Graph, error) {
	graphPath := filepath.Join(graphRoot, graphID, "graph.json")
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
	}, nil
}

func hasGraphFilter(f graphFilters) bool {
	return !f.IncludeInferred || f.EdgeTypeFilter != "" || f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != "" || f.ConfidenceMin > 0
}

func filterGraph(graph graphschema.Graph, f graphFilters) graphschema.Graph {
	edges := filterGraphEdges(graph, f)
	serviceRepo := map[string]string{}
	for _, s := range graph.Meta.Services {
		serviceRepo[s.ID] = s.RepoPath
	}

	// If there is no explicit node-scoping filter and edge filtering produced no edges,
	// preserve nodes so node-only graphs remain visible in the UI.
	hasNodeScope := f.ServiceFilter != "" || f.RepoFilter != "" || f.NodeFilter != ""
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
		if _, ok := includeNodes[n.ID]; ok {
			nodes = append(nodes, n)
		}
	}

	graph.Nodes = nodes
	graph.Edges = edges
	graph.Stats = recomputeGraphStats(nodes, edges)
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
		out = append(out, e)
	}
	return out
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
