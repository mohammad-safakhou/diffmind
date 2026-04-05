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
)

// Server serves the DiffMind graph visualization dashboard.
type Server struct {
	diffmindRunsDir  string
	serviceRepoDirs map[string]string // name -> repo path
	host            string
	port            int
}

// New creates a new UI server.
func New(diffmindRunsDir string, serviceRepoDirs map[string]string, host string, port int) *Server {
	if strings.TrimSpace(diffmindRunsDir) == "" {
		diffmindRunsDir = ".diffmind/runs"
	}
	if strings.TrimSpace(host) == "" {
		host = "127.0.0.1"
	}
	if port <= 0 {
		port = 8090
	}
	return &Server{diffmindRunsDir: diffmindRunsDir, serviceRepoDirs: serviceRepoDirs, host: host, port: port}
}

func (s *Server) Addr() string { return fmt.Sprintf("%s:%d", s.host, s.port) }

// Start begins serving. Blocks until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/runs", s.handleRuns)
	mux.HandleFunc("/api/graph/", s.handleGraph)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	srv := &http.Server{Addr: s.Addr(), Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("DiffMind dashboard: http://%s\n", s.Addr())
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

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func (s *Server) handleRuns(w http.ResponseWriter, _ *http.Request) {
	runs, err := listDirs(s.diffmindRunsDir)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"runs": runs})
}

// handleGraph builds the full architecture graph from DiffMind + DiffMind data.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimPrefix(r.URL.Path, "/api/graph/")
	runID = strings.TrimSpace(runID)
	if runID == "" || runID == "latest" {
		runs, _ := listDirs(s.diffmindRunsDir)
		if len(runs) > 0 {
			runID = runs[0]
		}
	}

	graph, err := s.buildArchitectureGraph(runID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, graph)
}

// ---- Architecture Graph Builder ----

type ArchGraph struct {
	RunID         string          `json:"run_id"`
	Services      []*ServiceNode  `json:"services"`
	ExternalNodes []*ExternalNode `json:"external_nodes"`
	QueueNodes    []*QueueNode    `json:"queue_nodes"`
	DatabaseNodes []*DatabaseNode `json:"database_nodes"`
	Edges         []*GraphEdge    `json:"edges"`
}

type ServiceNode struct {
	Name           string          `json:"name"`
	Known          bool            `json:"known"`
	HTTPRoutes     []EntitySummary `json:"http_routes"`
	QueueConsumers []EntitySummary `json:"queue_consumers"`
	ScheduledJobs  []EntitySummary `json:"scheduled_jobs"`
	Webhooks       []EntitySummary `json:"webhooks"`
	CLICommands    []EntitySummary `json:"cli_commands"`
	Databases      []string        `json:"databases"`
}

type ExternalNode struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "service", "api", "saas"
}

type QueueNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "sqs", "sns", "kafka", "kinesis"
	FIFO bool   `json:"fifo"`
}

type DatabaseNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "postgresql", "dynamodb", "athena", "redis", "elasticsearch"
	Host string `json:"host,omitempty"`
}

type GraphEdge struct {
	From       string          `json:"from"`
	FromPort   string          `json:"from_port"`
	To         string          `json:"to"`
	ToPort     string          `json:"to_port"`
	Type       string          `json:"type"` // "http", "queue_publish", "queue_consume", "db_read", "db_write", "cache"
	Label      string          `json:"label"`
	Details    []EntitySummary `json:"details"`
	Confidence float64         `json:"confidence"`
}

type EntitySummary struct {
	Name    string         `json:"name"`
	Summary string         `json:"summary"`
	Details map[string]any `json:"details,omitempty"`
}

func (s *Server) buildArchitectureGraph(runID string) (*ArchGraph, error) {
	g := &ArchGraph{RunID: runID}

	knownServices := map[string]bool{}
	allOutboundHTTP := map[string][]outboundRef{} // source svc -> targets
	allQueuePublish := map[string][]queueRef{}    // source svc -> queues
	allQueueConsume := map[string][]queueRef{}    // source svc -> queues
	allDBs := map[string][]dbRef{}                // source svc -> databases
	allCacheOps := map[string][]dbRef{}           // source svc -> caches

	// Phase 1: Load all DiffMind data per service
	for name, repoPath := range s.serviceRepoDirs {
		knownServices[name] = true
		exposures, dependencies := loadDiffMindData(repoPath)

		svc := &ServiceNode{
			Name:  name,
			Known: true,
		}

		// Extract exposures
		for _, item := range exposures["http_route"] {
			svc.HTTPRoutes = append(svc.HTTPRoutes, toSummary(item))
		}
		for _, item := range exposures["queue_consumer"] {
			svc.QueueConsumers = append(svc.QueueConsumers, toSummary(item))
			d := getDetails(item)
			qName := firstNonEmpty(d["queue"], d["queue_name"], d["destination"], getString(item, "name"))
			kind := inferQueueKind(qName, d)
			fifo := strings.Contains(strings.ToLower(qName), "fifo")
			allQueueConsume[name] = append(allQueueConsume[name], queueRef{name: qName, kind: kind, fifo: fifo})
		}
		for _, item := range exposures["scheduled_job"] {
			svc.ScheduledJobs = append(svc.ScheduledJobs, toSummary(item))
		}
		for _, item := range exposures["webhook"] {
			svc.Webhooks = append(svc.Webhooks, toSummary(item))
		}
		for _, item := range exposures["cli_command"] {
			svc.CLICommands = append(svc.CLICommands, toSummary(item))
		}

		// Extract dependencies
		for _, item := range dependencies["outbound_http"] {
			d := getDetails(item)
			target := firstNonEmpty(d["target_service"], d["target_url"], getString(item, "name"))
			allOutboundHTTP[name] = append(allOutboundHTTP[name], outboundRef{
				target:    normalizeServiceName(target),
				endpoints: toSummary(item),
			})
		}
		for _, item := range dependencies["queue_publish"] {
			d := getDetails(item)
			qName := firstNonEmpty(d["queue"], d["queue_name"], d["destination"], getString(item, "name"))
			kind := inferQueueKind(qName, d)
			fifo := strings.Contains(strings.ToLower(qName), "fifo")
			allQueuePublish[name] = append(allQueuePublish[name], queueRef{name: qName, kind: kind, fifo: fifo})
		}
		for _, item := range dependencies["db_operation"] {
			d := getDetails(item)
			dbType := strings.ToLower(firstNonEmpty(d["database_type"], d["type"], "database"))
			// Use entity name as display name (most descriptive), fall back to database_name
			dbName := firstNonEmpty(getString(item, "name"), d["database_name"], d["table"], d["entity"])
			host := firstNonEmpty(d["host_production"], d["host"])
			// Extract operations list for edge labels
			op := extractOperations(item)
			allDBs[name] = append(allDBs[name], dbRef{name: dbName, kind: normalizeDBKind(dbType), operation: op, host: host, summary: toSummary(item)})
			svc.Databases = append(svc.Databases, dbName)
		}
		for _, item := range dependencies["cache_operation"] {
			d := getDetails(item)
			cacheType := strings.ToLower(firstNonEmpty(d["cache_type"], d["database_type"], "redis"))
			cacheName := firstNonEmpty(getString(item, "name"), d["cache_name"], d["key_pattern"])
			op := extractOperations(item)
			if op == "read/write" {
				op = "cache"
			}
			allCacheOps[name] = append(allCacheOps[name], dbRef{name: cacheName, kind: cacheType, operation: op, summary: toSummary(item)})
		}

		g.Services = append(g.Services, svc)
	}

	// Phase 2: Discover external services, queues, databases
	externalSvcs := map[string]*ExternalNode{}
	queueMap := map[string]*QueueNode{}
	dbMap := map[string]*DatabaseNode{}

	// Databases
	for svcName, dbs := range allDBs {
		for _, db := range dbs {
			dbID := normalizeID(db.kind + "_" + db.name)
			if _, ok := dbMap[dbID]; !ok {
				dbMap[dbID] = &DatabaseNode{ID: dbID, Name: db.name, Kind: db.kind, Host: db.host}
			}
			g.Edges = append(g.Edges, &GraphEdge{
				From: svcName, To: "db:" + dbID, Type: "database",
				Label:   db.operation,
				Details: []EntitySummary{db.summary},
			})
		}
	}

	// Caches
	for svcName, ops := range allCacheOps {
		for _, op := range ops {
			dbID := normalizeID(op.kind + "_" + op.name)
			if _, ok := dbMap[dbID]; !ok {
				dbMap[dbID] = &DatabaseNode{ID: dbID, Name: op.name, Kind: op.kind}
			}
			g.Edges = append(g.Edges, &GraphEdge{
				From: svcName, To: "db:" + dbID, Type: "cache",
				Label: "cache",
			})
		}
	}

	// Queues - publish
	for svcName, pubs := range allQueuePublish {
		for _, q := range pubs {
			qID := normalizeID(q.name)
			if _, ok := queueMap[qID]; !ok {
				queueMap[qID] = &QueueNode{ID: qID, Name: q.name, Kind: q.kind, FIFO: q.fifo}
			}
			g.Edges = append(g.Edges, &GraphEdge{
				From: svcName, To: "queue:" + qID, Type: "queue_publish",
				Label: "publish",
			})
		}
	}

	// Queues - consume
	for svcName, cons := range allQueueConsume {
		for _, q := range cons {
			qID := normalizeID(q.name)
			if _, ok := queueMap[qID]; !ok {
				queueMap[qID] = &QueueNode{ID: qID, Name: q.name, Kind: q.kind, FIFO: q.fifo}
			}
			g.Edges = append(g.Edges, &GraphEdge{
				From: "queue:" + qID, To: svcName, Type: "queue_consume",
				Label: "consume",
			})
		}
	}

	// Outbound HTTP
	for svcName, targets := range allOutboundHTTP {
		for _, t := range targets {
			targetName := t.target
			if !knownServices[targetName] {
				if _, ok := externalSvcs[targetName]; !ok {
					kind := "service"
					lower := strings.ToLower(targetName)
					if strings.Contains(lower, "microsoft") || strings.Contains(lower, "salesforce") || strings.Contains(lower, "sentry") {
						kind = "saas"
					} else if strings.Contains(lower, "api") || strings.Contains(lower, "gateway") {
						kind = "api"
					}
					externalSvcs[targetName] = &ExternalNode{Name: targetName, Kind: kind}
				}
			}
			g.Edges = append(g.Edges, &GraphEdge{
				From: svcName, To: targetName, Type: "http",
				Label: "HTTP", Details: []EntitySummary{t.endpoints}, Confidence: 1.0,
			})
		}
	}

	// Phase 3: Assemble final lists
	for _, n := range externalSvcs {
		g.ExternalNodes = append(g.ExternalNodes, n)
	}
	sortExternal(g.ExternalNodes)

	for _, q := range queueMap {
		g.QueueNodes = append(g.QueueNodes, q)
	}
	sortQueues(g.QueueNodes)

	for _, d := range dbMap {
		g.DatabaseNodes = append(g.DatabaseNodes, d)
	}
	sortDatabases(g.DatabaseNodes)

	sortServices(g.Services)

	return g, nil
}

// ---- Internal Types ----

type outboundRef struct {
	target    string
	endpoints EntitySummary
}

type queueRef struct {
	name string
	kind string
	fifo bool
}

type dbRef struct {
	name      string
	kind      string
	operation string
	host      string
	summary   EntitySummary
}

// ---- Data Loading ----

func loadDiffMindData(repoPath string) (exposures map[string][]map[string]any, dependencies map[string][]map[string]any) {
	exposures = make(map[string][]map[string]any)
	dependencies = make(map[string][]map[string]any)

	runsDir := filepath.Join(repoPath, ".diffmind", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		return
	}
	// Pick latest run
	var latest string
	for _, e := range entries {
		if e.IsDir() && e.Name() > latest {
			latest = e.Name()
		}
	}
	if latest == "" {
		return
	}

	runDir := filepath.Join(runsDir, latest)
	exposures = readJSONDir(filepath.Join(runDir, "exposures"))
	dependencies = readJSONDir(filepath.Join(runDir, "dependencies"))
	return
}

func readJSONDir(dir string) map[string][]map[string]any {
	out := make(map[string][]map[string]any)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var items []map[string]any
		if json.Unmarshal(b, &items) == nil {
			out[strings.TrimSuffix(e.Name(), ".json")] = items
		}
	}
	return out
}

// ---- Helpers ----

// extractOperations gets a human-readable operation label from a DB entity.
func extractOperations(item map[string]any) string {
	d := getMap(item, "details")
	if d == nil {
		return "read/write"
	}
	// Check for operations array
	if ops, ok := d["operations"]; ok {
		switch v := ops.(type) {
		case []any:
			parts := make([]string, 0, len(v))
			for _, o := range v {
				if s, ok := o.(string); ok {
					parts = append(parts, strings.ToLower(s))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", ")
			}
		case string:
			return v
		}
	}
	// Fall back to single operation field
	if op, ok := d["operation"]; ok {
		if s, ok := op.(string); ok && s != "" {
			return s
		}
	}
	return "read/write"
}

func toSummary(item map[string]any) EntitySummary {
	return EntitySummary{
		Name:    getString(item, "name"),
		Summary: getString(item, "summary"),
		Details: getMap(item, "details"),
	}
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key]; ok {
		if m2, ok := v.(map[string]any); ok {
			return m2
		}
	}
	return nil
}

func getDetails(item map[string]any) map[string]string {
	out := map[string]string{}
	d := getMap(item, "details")
	for k, v := range d {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeServiceName(raw string) string {
	raw = strings.TrimSpace(raw)
	// Strip URL parts
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimPrefix(raw, prefix)
			// Take the hostname part
			if idx := strings.Index(raw, "/"); idx > 0 {
				raw = raw[:idx]
			}
			// Strip common suffixes
			for _, suffix := range []string{".example.global", ".example.biz", ".lead2cash.svc.cluster.local", "-default.lead2cash.svc.cluster.local", "-default.data"} {
				raw = strings.TrimSuffix(raw, suffix)
			}
		}
	}
	// Clean up
	raw = strings.TrimRight(raw, "/")
	return raw
}

func normalizeID(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, s)
	return s
}

func normalizeDBKind(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "postgres"):
		return "postgresql"
	case strings.Contains(lower, "dynamo"):
		return "dynamodb"
	case strings.Contains(lower, "redis"):
		return "redis"
	case strings.Contains(lower, "athena"):
		return "athena"
	case strings.Contains(lower, "elastic") || strings.Contains(lower, "opensearch"):
		return "elasticsearch"
	case strings.Contains(lower, "mongo"):
		return "mongodb"
	default:
		return lower
	}
}

func inferQueueKind(name string, d map[string]string) string {
	lower := strings.ToLower(name + " " + d["kind"] + " " + d["type"])
	switch {
	case strings.Contains(lower, "sqs"):
		return "sqs"
	case strings.Contains(lower, "sns"):
		return "sns"
	case strings.Contains(lower, "kafka"):
		return "kafka"
	case strings.Contains(lower, "kinesis"):
		return "kinesis"
	case strings.Contains(lower, "rabbit"):
		return "rabbitmq"
	default:
		return "queue"
	}
}

func listDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0)
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func sortServices(s []*ServiceNode) {
	sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}
func sortExternal(s []*ExternalNode) {
	sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}
func sortQueues(s []*QueueNode) {
	sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}
func sortDatabases(s []*DatabaseNode) {
	sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	writeJSON(w, map[string]any{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
