package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"diffmind/internal/bundleio"
	"diffmind/internal/config"
	"diffmind/internal/facts"
	"diffmind/internal/graphschema"
	"diffmind/internal/store"
	"go.yaml.in/yaml/v3"
)

type serviceSpec struct {
	ID             string   `json:"id" yaml:"id"`
	Name           string   `json:"name" yaml:"name"`
	TenantID       string   `json:"tenant_id" yaml:"tenant_id"`
	RepoPath       string   `json:"repo_path" yaml:"repo_path"`
	BundlePath     string   `json:"bundle_path" yaml:"bundle_path"`
	AnalyzerBundle string   `json:"analyzer_bundle_path" yaml:"analyzer_bundle_path"`
	BaseURLs       []string `json:"base_urls" yaml:"base_urls"`
	QueuePublishes []string `json:"queue_publishes" yaml:"queue_publishes"`
	QueueConsumes  []string `json:"queue_consumes" yaml:"queue_consumes"`
	DBReads        []string `json:"db_reads" yaml:"db_reads"`
	DBWrites       []string `json:"db_writes" yaml:"db_writes"`
}

type manifest struct {
	Services []serviceSpec `json:"services" yaml:"services"`
}

type options struct {
	ManifestPath       string
	SourcesCSV         string
	OutDir             string
	Persist            bool
	TenantID           string
	ServiceID          string
	ServiceName        string
	BundlePath         string
	AnalyzerBundlePath string
	BaseURLs           string
	Mode               string
}

type BuildRequest struct {
	ManifestPath       string   `json:"manifest_path"`
	Sources            []string `json:"sources"`
	OutDir             string   `json:"out_dir"`
	Persist            bool     `json:"persist"`
	TenantID           string   `json:"tenant_id"`
	Mode               string   `json:"mode"`
	ServiceID          string   `json:"service_id"`
	ServiceName        string   `json:"service_name"`
	BundlePath         string   `json:"bundle_path"`
	AnalyzerBundlePath string   `json:"analyzer_bundle_path"`
	BaseURLs           []string `json:"base_urls"`
}

type BuildResult struct {
	GraphID     string    `json:"graph_id"`
	GraphPath   string    `json:"graph_path"`
	IndexPath   string    `json:"index_path"`
	Mode        string    `json:"mode"`
	NodeCount   int       `json:"node_count"`
	EdgeCount   int       `json:"edge_count"`
	GeneratedAt time.Time `json:"generated_at"`
}

func Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("graph subcommand is required: build|discover")
	}

	switch strings.ToLower(args[0]) {
	case "build":
		return runBuild(ctx, args[1:])
	case "discover":
		return runDiscover(ctx, args[1:])
	default:
		return fmt.Errorf("unsupported graph subcommand %q", args[0])
	}
}

func runBuild(ctx context.Context, args []string) error {
	opts, err := parseBuildOptions(args)
	if err != nil {
		return err
	}

	_, err = buildFromOptions(ctx, opts, true)
	return err
}

func Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	baseURLs := splitCSV(strings.Join(req.BaseURLs, ","))
	opts := options{
		ManifestPath:       strings.TrimSpace(req.ManifestPath),
		SourcesCSV:         strings.Join(splitCSV(strings.Join(req.Sources, ",")), ","),
		OutDir:             strings.TrimSpace(req.OutDir),
		Persist:            req.Persist,
		TenantID:           strings.TrimSpace(req.TenantID),
		ServiceID:          strings.TrimSpace(req.ServiceID),
		ServiceName:        strings.TrimSpace(req.ServiceName),
		BundlePath:         strings.TrimSpace(req.BundlePath),
		AnalyzerBundlePath: strings.TrimSpace(req.AnalyzerBundlePath),
		BaseURLs:           strings.Join(baseURLs, ","),
		Mode:               strings.ToLower(strings.TrimSpace(req.Mode)),
	}
	if opts.Mode == "" {
		opts.Mode = "auto"
	}
	if opts.OutDir == "" {
		opts.OutDir = ".diffmind"
	}
	if opts.TenantID == "" {
		opts.TenantID = "default"
	}
	if opts.ManifestPath == "" {
		opts.ManifestPath = filepath.Join("graph", "services.yaml")
	}
	if opts.BundlePath == "" {
		opts.BundlePath = filepath.Join(".diffmind", "bundle", "intelligence_bundle.json")
	}
	if opts.AnalyzerBundlePath == "" {
		opts.AnalyzerBundlePath = filepath.Join(".diffmind", "analyzers", "bundle.json")
	}

	return buildFromOptions(ctx, opts, false)
}

func buildFromOptions(ctx context.Context, opts options, printPath bool) (BuildResult, error) {
	services, mode, err := resolveServices(opts)
	if err != nil {
		return BuildResult{}, err
	}
	for i := range services {
		if strings.TrimSpace(services[i].TenantID) == "" {
			services[i].TenantID = normalizeTenantID(opts.TenantID)
		} else {
			services[i].TenantID = normalizeTenantID(services[i].TenantID)
		}
	}

	graph, err := buildGraph(services, mode)
	if err != nil {
		return BuildResult{}, err
	}
	if reused, ok := findReusableGraph(opts.OutDir, graph); ok {
		if printPath {
			fmt.Println(reused.Path)
		}
		return BuildResult{
			GraphID:     reused.GraphID,
			GraphPath:   reused.Path,
			IndexPath:   filepath.Join(opts.OutDir, "graph", "index.json"),
			Mode:        reused.Mode,
			NodeCount:   reused.NodeCount,
			EdgeCount:   reused.EdgeCount,
			GeneratedAt: reused.GeneratedAt,
		}, nil
	}

	graphPath, err := writeGraph(opts.OutDir, graph)
	if err != nil {
		return BuildResult{}, err
	}
	if err := updateGraphIndex(opts.OutDir, graph, graphPath); err != nil {
		return BuildResult{}, err
	}

	if opts.Persist {
		cfg, err := config.LoadFromEnv()
		if err != nil {
			return BuildResult{}, fmt.Errorf("load config for graph persistence: %w", err)
		}
		db, err := store.NewPostgresDB(ctx, cfg.PostgresURL)
		if err != nil {
			return BuildResult{}, err
		}
		defer func() { _ = db.Close() }()
		gstore := store.NewGraphStore(db)
		if err := gstore.PersistGraph(ctx, graph, graphPath); err != nil {
			return BuildResult{}, err
		}
	}

	if printPath {
		fmt.Println(graphPath)
	}
	return BuildResult{
		GraphID:     graph.GraphID,
		GraphPath:   graphPath,
		IndexPath:   filepath.Join(opts.OutDir, "graph", "index.json"),
		Mode:        graph.Mode,
		NodeCount:   len(graph.Nodes),
		EdgeCount:   len(graph.Edges),
		GeneratedAt: graph.GeneratedAt,
	}, nil
}

func parseBuildOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("graph build", flag.ContinueOnError)
	manifestPath := fs.String("manifest", filepath.Join("graph", "services.yaml"), "Service registry manifest path")
	sources := fs.String("sources", "", "Comma-separated extraction output paths (or parent dirs to auto-discover services)")
	outDir := fs.String("out", ".diffmind", "Output root for graph artifacts")
	persist := fs.Bool("persist", false, "Persist graph into Postgres")
	serviceID := fs.String("service-id", "", "Single-repo service id")
	serviceName := fs.String("service-name", "", "Single-repo service name")
	tenantID := fs.String("tenant-id", "default", "Tenant id for graph ownership and policy isolation")
	bundlePath := fs.String("bundle", filepath.Join(".diffmind", "bundle", "intelligence_bundle.json"), "Single-repo bundle path")
	analyzerBundlePath := fs.String("analyzer-bundle", filepath.Join(".diffmind", "analyzers", "bundle.json"), "Single-repo analyzer bundle path")
	baseURLs := fs.String("base-urls", "", "Comma-separated base URLs for single-repo mode")
	mode := fs.String("mode", "auto", "Build mode: auto|single|multi")

	if err := fs.Parse(filterBuildArgs(args)); err != nil {
		return options{}, fmt.Errorf("parse graph build flags: %w", err)
	}

	return options{
		ManifestPath:       strings.TrimSpace(*manifestPath),
		SourcesCSV:         strings.TrimSpace(*sources),
		OutDir:             strings.TrimSpace(*outDir),
		Persist:            *persist,
		TenantID:           normalizeTenantID(strings.TrimSpace(*tenantID)),
		ServiceID:          strings.TrimSpace(*serviceID),
		ServiceName:        strings.TrimSpace(*serviceName),
		BundlePath:         strings.TrimSpace(*bundlePath),
		AnalyzerBundlePath: strings.TrimSpace(*analyzerBundlePath),
		BaseURLs:           strings.TrimSpace(*baseURLs),
		Mode:               strings.ToLower(strings.TrimSpace(*mode)),
	}, nil
}

func filterBuildArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--manifest" || arg == "--out" || arg == "--service-id" || arg == "--service-name" || arg == "--tenant-id" ||
			arg == "--bundle" || arg == "--analyzer-bundle" || arg == "--base-urls" || arg == "--mode" || arg == "--sources":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case arg == "--persist":
			out = append(out, arg)
		case strings.HasPrefix(arg, "--manifest=") || strings.HasPrefix(arg, "--out=") ||
			strings.HasPrefix(arg, "--service-id=") || strings.HasPrefix(arg, "--service-name=") || strings.HasPrefix(arg, "--tenant-id=") ||
			strings.HasPrefix(arg, "--bundle=") || strings.HasPrefix(arg, "--analyzer-bundle=") ||
			strings.HasPrefix(arg, "--base-urls=") || strings.HasPrefix(arg, "--mode=") ||
			strings.HasPrefix(arg, "--sources=") ||
			strings.HasPrefix(arg, "--persist="):
			out = append(out, arg)
		}
	}
	return out
}

func resolveServices(opts options) ([]serviceSpec, string, error) {
	mode := opts.Mode
	if mode != "auto" && mode != "single" && mode != "multi" {
		return nil, "", fmt.Errorf("unsupported --mode %q", opts.Mode)
	}

	manifestExists := false
	if opts.ManifestPath != "" {
		if st, err := os.Stat(opts.ManifestPath); err == nil && !st.IsDir() {
			manifestExists = true
		}
	}

	switch mode {
	case "single":
		svc := singleServiceFromOptions(opts)
		return []serviceSpec{svc}, "single", nil
	case "multi":
		if strings.TrimSpace(opts.SourcesCSV) != "" {
			svcs, err := discoverServices(splitCSV(opts.SourcesCSV))
			return svcs, "multi", err
		}
		svcs, err := loadManifestServices(opts.ManifestPath)
		return svcs, "multi", err
	case "auto":
		if strings.TrimSpace(opts.SourcesCSV) != "" {
			svcs, err := discoverServices(splitCSV(opts.SourcesCSV))
			return svcs, "multi", err
		}
		if manifestExists {
			svcs, err := loadManifestServices(opts.ManifestPath)
			return svcs, "multi", err
		}
		svc := singleServiceFromOptions(opts)
		return []serviceSpec{svc}, "single", nil
	default:
		return nil, "", fmt.Errorf("unsupported mode %q", mode)
	}
}

func singleServiceFromOptions(opts options) serviceSpec {
	id := opts.ServiceID
	if id == "" {
		id = "service.local"
	}
	name := opts.ServiceName
	if name == "" {
		name = id
	}
	baseURLs := splitCSV(opts.BaseURLs)
	return serviceSpec{
		ID:             id,
		Name:           name,
		TenantID:       normalizeTenantID(opts.TenantID),
		BundlePath:     opts.BundlePath,
		AnalyzerBundle: opts.AnalyzerBundlePath,
		BaseURLs:       baseURLs,
	}
}

func loadManifestServices(path string) ([]serviceSpec, error) {
	if path == "" {
		return nil, errors.New("manifest path is required in multi mode")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m manifest
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("decode yaml manifest: %w", err)
		}
	default:
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("decode json manifest: %w", err)
		}
	}
	if len(m.Services) == 0 {
		return nil, errors.New("manifest has no services")
	}
	for i := range m.Services {
		if strings.TrimSpace(m.Services[i].ID) == "" {
			return nil, fmt.Errorf("manifest service[%d] missing id", i)
		}
		if strings.TrimSpace(m.Services[i].Name) == "" {
			m.Services[i].Name = m.Services[i].ID
		}
		if strings.TrimSpace(m.Services[i].BundlePath) == "" {
			return nil, fmt.Errorf("manifest service[%d] missing bundle_path", i)
		}
		m.Services[i].TenantID = normalizeTenantID(m.Services[i].TenantID)
	}
	return m.Services, nil
}

func normalizeTenantID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func writeGraph(outDir string, graph graphschema.Graph) (string, error) {
	graphDir := filepath.Join(outDir, "graph", graph.GraphID)
	if err := os.MkdirAll(graphDir, 0o755); err != nil {
		return "", fmt.Errorf("create graph output dir: %w", err)
	}
	path := filepath.Join(graphDir, "graph.json")
	data, err := json.MarshalIndent(graph, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal graph: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write graph %s: %w", path, err)
	}
	return path, nil
}

func updateGraphIndex(outDir string, graph graphschema.Graph, graphPath string) error {
	indexPath := filepath.Join(outDir, "graph", "index.json")
	index := graphschema.Index{Graphs: []graphschema.Summary{}}
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(data, &index)
	}

	filtered := make([]graphschema.Summary, 0, len(index.Graphs)+1)
	for _, s := range index.Graphs {
		if s.GraphID != graph.GraphID {
			filtered = append(filtered, s)
		}
	}
	filtered = append(filtered, graphschema.Summary{
		GraphID:     graph.GraphID,
		GeneratedAt: graph.GeneratedAt,
		TenantID:    normalizeTenantID(graph.Meta.TenantID),
		Mode:        graph.Mode,
		NodeCount:   len(graph.Nodes),
		EdgeCount:   len(graph.Edges),
		Fingerprint: graph.Meta.Provenance.Fingerprint,
		Path:        graphPath,
	})
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].GeneratedAt.After(filtered[j].GeneratedAt)
	})
	index.Graphs = filtered

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal graph index: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return fmt.Errorf("create graph index dir: %w", err)
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		return fmt.Errorf("write graph index: %w", err)
	}
	return nil
}

type serviceInput struct {
	spec          serviceSpec
	bundle        bundleio.Bundle
	analyzer      facts.Bundle
	runtimeByID   map[string]bundleio.Entity
	endpointNodes map[string]string
}

type runReportMeta struct {
	Path   string
	Source string
	Ref    string
}

type snapshotMeta struct {
	Path        string
	RepoLocator string
	Ref         string
	CommitSHA   string
	SourceType  string
}

type graphBuilder struct {
	mode       string
	services   []serviceInput
	nodeByID   map[string]graphschema.Node
	edgeByID   map[string]graphschema.Edge
	byTypeNode map[string]int
	byTypeEdge map[string]int
}

func buildGraph(services []serviceSpec, mode string) (graphschema.Graph, error) {
	graphTenant := "default"
	if len(services) > 0 {
		graphTenant = normalizeTenantID(services[0].TenantID)
	}
	for _, svc := range services {
		if normalizeTenantID(svc.TenantID) != graphTenant {
			return graphschema.Graph{}, fmt.Errorf("all services in a graph must share the same tenant_id")
		}
	}

	inputs := make([]serviceInput, 0, len(services))
	for _, spec := range services {
		b, err := bundleio.Load(spec.BundlePath)
		if err != nil {
			return graphschema.Graph{}, err
		}
		var analyzer facts.Bundle
		if strings.TrimSpace(spec.AnalyzerBundle) != "" {
			data, err := os.ReadFile(spec.AnalyzerBundle)
			if err == nil {
				_ = json.Unmarshal(data, &analyzer)
			}
		}
		runtimeByID := map[string]bundleio.Entity{}
		for _, e := range b.Entities {
			if e.Type == "RuntimeUnit" {
				runtimeByID[e.ID] = e
			}
		}
		inputs = append(inputs, serviceInput{
			spec:          spec,
			bundle:        b,
			analyzer:      analyzer,
			runtimeByID:   runtimeByID,
			endpointNodes: map[string]string{},
		})
	}

	gb := &graphBuilder{
		mode:       mode,
		services:   inputs,
		nodeByID:   map[string]graphschema.Node{},
		edgeByID:   map[string]graphschema.Edge{},
		byTypeNode: map[string]int{},
		byTypeEdge: map[string]int{},
	}

	gb.addServiceNodes()
	gb.addEndpointNodes()
	gb.resolveAPIEdges()
	gb.resolveCodeQueueAndDBEdges()
	gb.resolveManifestQueueAndDBEdges()
	gb.resolveRuntimeBuildDeployEdges()
	gb.resolveConfigAndSensitiveEdges()
	gb.resolveDependencyOwnershipEdges()
	gb.resolveCrossRepoCanonicalization()
	gb.resolveConfidenceAndConflictEdges()
	gb.resolveVerificationDecisionEdges()

	nodes := make([]graphschema.Node, 0, len(gb.nodeByID))
	for _, n := range gb.nodeByID {
		nodes = append(nodes, n)
	}

	edges := make([]graphschema.Edge, 0, len(gb.edgeByID))
	for _, e := range gb.edgeByID {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	nodes = trimUnconnectedNodes(nodes, edges)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	byNode := countNodeTypes(nodes)
	byEdge := countEdgeTypes(edges)

	graphID := fmt.Sprintf("%d", time.Now().UTC().UnixNano())
	metaServices := make([]graphschema.ServiceMeta, 0, len(inputs))
	for _, s := range inputs {
		outputRoot := deriveOutputRootFromBundlePath(s.spec.BundlePath)
		runMeta := discoverRunReportMetadata(outputRoot)
		snapMeta := discoverSnapshotMetadata(outputRoot, s.bundle.SnapshotID)
		bundleSHA, _ := fileSHA256(s.spec.BundlePath)
		analyzerSHA, _ := fileSHA256(s.spec.AnalyzerBundle)

		metaServices = append(metaServices, graphschema.ServiceMeta{
			ID:             s.spec.ID,
			Name:           s.spec.Name,
			RepoPath:       s.spec.RepoPath,
			BundlePath:     s.spec.BundlePath,
			AnalyzerBundle: s.spec.AnalyzerBundle,
			BaseURLs:       append([]string(nil), s.spec.BaseURLs...),
			QueuePublishes: append([]string(nil), s.spec.QueuePublishes...),
			QueueConsumes:  append([]string(nil), s.spec.QueueConsumes...),
			DBReads:        append([]string(nil), s.spec.DBReads...),
			DBWrites:       append([]string(nil), s.spec.DBWrites...),
			Provenance: graphschema.ServiceProvenance{
				OutputRoot:           outputRoot,
				RunReportPath:        runMeta.Path,
				RunSource:            runMeta.Source,
				RunRef:               runMeta.Ref,
				SnapshotID:           s.bundle.SnapshotID,
				SnapshotPath:         snapMeta.Path,
				SnapshotRepoLocator:  snapMeta.RepoLocator,
				SnapshotRef:          snapMeta.Ref,
				SnapshotCommitSHA:    snapMeta.CommitSHA,
				SnapshotSourceType:   snapMeta.SourceType,
				BundleSHA256:         bundleSHA,
				AnalyzerBundleSHA256: analyzerSHA,
			},
		})
	}
	sort.Slice(metaServices, func(i, j int) bool { return metaServices[i].ID < metaServices[j].ID })
	graph := graphschema.Graph{
		GraphID:     graphID,
		GeneratedAt: time.Now().UTC(),
		Mode:        mode,
		Nodes:       nodes,
		Edges:       edges,
		Stats: graphschema.GraphStats{
			NodeCount: len(nodes),
			EdgeCount: len(edges),
			ByNode:    byNode,
			ByEdge:    byEdge,
		},
		Meta: graphschema.GraphMeta{
			OntologyVersion: graphschema.OntologyVersionV2,
			TenantID:        graphTenant,
			Services:        metaServices,
			Provenance: graphschema.GraphProvenance{
				Tool:        "diffmind",
				GeneratedBy: "graph.build",
				Fingerprint: "",
			},
		},
	}
	graphschema.NormalizeGraphSemantics(&graph)
	graph.Meta.Provenance.Fingerprint = computeGraphFingerprint(
		graph.Mode,
		graph.Meta.OntologyVersion,
		graph.Nodes,
		graph.Edges,
		graph.Meta.Services,
	)
	if err := graphschema.ValidateGraph(graph); err != nil {
		return graphschema.Graph{}, fmt.Errorf("validate graph schema: %w", err)
	}
	return graph, nil
}

func computeGraphFingerprint(mode string, ontologyVersion string, nodes []graphschema.Node, edges []graphschema.Edge, services []graphschema.ServiceMeta) string {
	payload := struct {
		Mode            string                    `json:"mode"`
		OntologyVersion string                    `json:"ontology_version"`
		Nodes           []graphschema.Node        `json:"nodes"`
		Edges           []graphschema.Edge        `json:"edges"`
		Services        []graphschema.ServiceMeta `json:"services"`
	}{
		Mode:            mode,
		OntologyVersion: ontologyVersion,
		Nodes:           nodes,
		Edges:           edges,
		Services:        services,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func findReusableGraph(outDir string, graph graphschema.Graph) (graphschema.Summary, bool) {
	indexPath := filepath.Join(outDir, "graph", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return graphschema.Summary{}, false
	}
	var index graphschema.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return graphschema.Summary{}, false
	}
	if len(index.Graphs) == 0 {
		return graphschema.Summary{}, false
	}
	latest := index.Graphs[0]
	if strings.TrimSpace(latest.Fingerprint) == "" || strings.TrimSpace(graph.Meta.Provenance.Fingerprint) == "" {
		return graphschema.Summary{}, false
	}
	if latest.Mode != graph.Mode {
		return graphschema.Summary{}, false
	}
	if normalizeTenantID(latest.TenantID) != normalizeTenantID(graph.Meta.TenantID) {
		return graphschema.Summary{}, false
	}
	if latest.Fingerprint != graph.Meta.Provenance.Fingerprint {
		return graphschema.Summary{}, false
	}
	if _, err := os.Stat(latest.Path); err != nil {
		return graphschema.Summary{}, false
	}
	return latest, true
}

func trimUnconnectedNodes(nodes []graphschema.Node, edges []graphschema.Edge) []graphschema.Node {
	if len(nodes) == 0 {
		return nodes
	}
	connected := map[string]struct{}{}
	for _, e := range edges {
		connected[e.SourceID] = struct{}{}
		connected[e.TargetID] = struct{}{}
	}
	out := make([]graphschema.Node, 0, len(nodes))
	for _, n := range nodes {
		// Always keep service nodes as top-level architecture anchors.
		if n.Type == "service" {
			out = append(out, n)
			continue
		}
		if _, ok := connected[n.ID]; ok {
			out = append(out, n)
		}
	}
	return out
}

func countNodeTypes(nodes []graphschema.Node) map[string]int {
	out := map[string]int{}
	for _, n := range nodes {
		out[n.Type]++
	}
	return out
}

func countEdgeTypes(edges []graphschema.Edge) map[string]int {
	out := map[string]int{}
	for _, e := range edges {
		out[e.Type]++
	}
	return out
}

func deriveOutputRootFromBundlePath(bundlePath string) string {
	trimmed := strings.TrimSpace(bundlePath)
	if trimmed == "" {
		return ""
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		abs = filepath.Clean(trimmed)
	}
	parent := filepath.Base(filepath.Dir(abs))
	if parent == "bundle" {
		return filepath.Dir(filepath.Dir(abs))
	}
	return filepath.Dir(abs)
}

func discoverRunReportMetadata(outputRoot string) runReportMeta {
	if strings.TrimSpace(outputRoot) == "" {
		return runReportMeta{}
	}
	path := filepath.Join(outputRoot, "run", "report.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return runReportMeta{}
	}
	var report struct {
		Source string `json:"source"`
		Ref    string `json:"ref"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return runReportMeta{}
	}
	return runReportMeta{
		Path:   path,
		Source: strings.TrimSpace(report.Source),
		Ref:    strings.TrimSpace(report.Ref),
	}
}

func discoverSnapshotMetadata(outputRoot string, snapshotID string) snapshotMeta {
	if strings.TrimSpace(outputRoot) == "" || strings.TrimSpace(snapshotID) == "" {
		return snapshotMeta{}
	}
	path := filepath.Join(outputRoot, "snapshots", snapshotID, "snapshot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return snapshotMeta{}
	}
	var meta struct {
		RepoLocator string `json:"repo_locator"`
		Ref         string `json:"ref"`
		CommitSHA   string `json:"commit_sha"`
		SourceType  string `json:"source_type"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return snapshotMeta{}
	}
	return snapshotMeta{
		Path:        path,
		RepoLocator: strings.TrimSpace(meta.RepoLocator),
		Ref:         strings.TrimSpace(meta.Ref),
		CommitSHA:   strings.TrimSpace(meta.CommitSHA),
		SourceType:  strings.TrimSpace(meta.SourceType),
	}
}

func fileSHA256(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	f, err := os.Open(trimmed)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
