package archgraph

// Architecture graph view. This builds a rich, dependency-typed drill-down
// graph directly from the DiffMind artifacts bound to a run. It is separate
// from graph.json, which is built from service identities and dependency
// targets.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"gopkg.in/yaml.v3"
)

// ArchGraph is the architecture graph for a single run.
type ArchGraph struct {
	RunID          string           `json:"run_id"`
	Services       []*ServiceNode   `json:"services"`
	ExternalNodes  []*ExternalNode  `json:"external_nodes"`
	QueueNodes     []*QueueNode     `json:"queue_nodes"`
	DatabaseNodes  []*DatabaseNode  `json:"database_nodes"`
	SchedulerNodes []*SchedulerNode `json:"scheduler_nodes"`
	Edges          []*GraphEdge     `json:"edges"`
}

type SchedulerNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Service  string `json:"service"`
	Schedule string `json:"schedule"`
	Profile  string `json:"profile,omitempty"`
}

type ServiceNode struct {
	Name              string              `json:"name"`
	Known             bool                `json:"known"`
	RepoID            string              `json:"repo_id,omitempty"`
	RepoPath          string              `json:"repo_path,omitempty"`
	Team              string              `json:"team,omitempty"`
	DiffMindFreshness string              `json:"diffmind_freshness,omitempty"`
	RepoMetrics       *model.RepoMetrics  `json:"repo_metrics,omitempty"`
	HTTPRoutes        []EntitySummary     `json:"http_routes"`
	QueueConsumers    []EntitySummary     `json:"queue_consumers"`
	ScheduledJobs     []EntitySummary     `json:"scheduled_jobs"`
	Webhooks          []EntitySummary     `json:"webhooks"`
	CLICommands       []EntitySummary     `json:"cli_commands"`
	Databases         []string            `json:"databases"`
	Dependencies      []EntitySummary     `json:"dependencies"`
	Connections       []ConnectionSummary `json:"connections"`
}

type ConnectionSummary struct {
	FromName string `json:"from_name"`
	FromType string `json:"from_type"`
	ToName   string `json:"to_name"`
	ToType   string `json:"to_type"`
	Summary  string `json:"summary"`
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
	Type       string          `json:"type"` // "http", "queue_publish", "queue_consume", "database", "cache", "scheduler"
	Label      string          `json:"label"`
	Details    []EntitySummary `json:"details"`
	Confidence float64         `json:"confidence"`
}

type EntitySummary struct {
	Name    string         `json:"name"`
	Summary string         `json:"summary"`
	Details map[string]any `json:"details,omitempty"`
}

// Build builds the architecture graph from DiffMind artifacts.
// serviceRepoDirs maps a service name to the DiffMind run directory whose
// exposures/dependencies/connections describe that service.
func Build(runID string, serviceRepoDirs map[string]string) *ArchGraph {
	g := &ArchGraph{RunID: runID}

	knownServices := serviceAliasMap(serviceRepoDirs)
	allOutboundHTTP := map[string][]outboundRef{} // source svc -> targets
	allQueuePublish := map[string][]queueRef{}    // source svc -> queues
	allQueueConsume := map[string][]queueRef{}    // source svc -> queues
	allDBs := map[string][]dbRef{}                // source svc -> databases
	allCacheOps := map[string][]dbRef{}           // source svc -> caches

	// Phase 1: Load all DiffMind data per service
	for name, diffmindDir := range serviceRepoDirs {
		exposures, dependencies, connections := loadDiffMindData(diffmindDir)

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
			qName := firstNonEmpty(d["stream_arn"], d["event_source_arn"], d["source"], d["destination"], d["topic"], d["queue"], d["queue_name"], getString(item, "instance"), getString(item, "name"))
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
			target := graphHTTPTarget(item)
			allOutboundHTTP[name] = append(allOutboundHTTP[name], outboundRef{
				target:    target,
				endpoints: toSummary(item),
			})
		}
		for _, item := range dependencies["queue_publish"] {
			d := getDetails(item)
			qName := firstNonEmpty(d["stream_arn"], d["event_source_arn"], d["source"], d["destination"], d["topic"], d["queue"], d["queue_name"], getString(item, "instance"), getString(item, "name"))
			kind := inferQueueKind(qName, d)
			fifo := strings.Contains(strings.ToLower(qName), "fifo")
			allQueuePublish[name] = append(allQueuePublish[name], queueRef{name: qName, kind: kind, fifo: fifo})
		}
		for _, item := range dependencies["db_operation"] {
			d := getDetails(item)
			rawDetails := getMap(item, "details")
			target := getMap(rawDetails, "target")
			metadataDetails := getMap(getMap(rawDetails, "metadata"), "details")
			dbType := strings.ToLower(firstKnown(
				d["database_type"],
				d["engine"],
				d["platform"],
				getString(item, "platform"),
				getString(metadataDetails, "database_type"),
				d["type"],
				"database",
			))
			dbKind := normalizeDBKind(dbType)
			// Postgres/MySQL-style dependencies are shown at datasource/database
			// level, while DynamoDB is table-shaped and should use table identity.
			dbName := graphDBName(dbKind, item, d, target, metadataDetails)
			host := firstNonEmpty(d["host_production"], d["host"])
			// Extract operations list for edge labels
			op := extractOperations(item)
			allDBs[name] = append(allDBs[name], dbRef{name: dbName, kind: dbKind, operation: op, host: host, summary: toSummary(item)})
			svc.Databases = append(svc.Databases, dbName)
		}
		for _, item := range dependencies["cache_operation"] {
			d := getDetails(item)
			cacheName := graphCacheName(item)
			cacheType := normalizeCacheKind(firstNonEmpty(d["cache_type"], d["platform"], getString(item, "platform"), d["database_type"], "cache"), cacheName)
			op := extractOperations(item)
			if op == "read/write" {
				op = "cache"
			}
			allCacheOps[name] = append(allCacheOps[name], dbRef{name: cacheName, kind: cacheType, operation: op, summary: toSummary(item)})
		}

		// Collect raw dependencies for sidebar display
		for _, depType := range []string{"outbound_http", "queue_publish", "db_operation", "cache_operation"} {
			for _, item := range dependencies[depType] {
				svc.Dependencies = append(svc.Dependencies, toSummary(item))
			}
		}

		// Build connection summaries by matching IDs
		expByID := map[string]string{}
		for _, expType := range []string{"http_route", "queue_consumer", "scheduled_job", "webhook", "cli_command"} {
			for _, item := range exposures[expType] {
				if id := getString(item, "id"); id != "" {
					expByID[id] = getString(item, "name")
				}
			}
		}
		depByID := map[string]string{}
		for _, depType := range []string{"outbound_http", "queue_publish", "db_operation", "cache_operation"} {
			for _, item := range dependencies[depType] {
				if id := getString(item, "id"); id != "" {
					depByID[id] = getString(item, "name")
				}
			}
		}
		for _, connItems := range connections {
			for _, c := range connItems {
				fromID := getString(c, "from_exposure_id")
				toID := getString(c, "to_dependency_id")
				fromName := expByID[fromID]
				toName := depByID[toID]
				if fromName == "" {
					fromName = fromID
				}
				if toName == "" {
					toName = toID
				}
				svc.Connections = append(svc.Connections, ConnectionSummary{
					FromName: fromName,
					FromType: getString(c, "from_type"),
					ToName:   toName,
					ToType:   getString(c, "to_type"),
					Summary:  getString(c, "summary"),
				})
			}
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
				Label:   op.operation,
				Details: []EntitySummary{op.summary},
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
			if canonical, ok := canonicalKnownService(knownServices, targetName); ok {
				targetName = canonical
			} else {
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

	// Scheduler nodes
	for _, svc := range g.Services {
		if !svc.Known {
			continue
		}
		for _, job := range svc.ScheduledJobs {
			d := job.Details
			schedule := ""
			profile := ""
			if d != nil {
				if s, ok := d["schedule"]; ok {
					if str, ok := s.(string); ok {
						schedule = str
					}
				}
				if s, ok := d["cron"]; ok {
					if str, ok := s.(string); ok && schedule == "" {
						schedule = str
					}
				}
				if s, ok := d["spring_profile"]; ok {
					if str, ok := s.(string); ok {
						profile = str
					}
				}
				if s, ok := d["k8s_cronjob_name"]; ok {
					if str, ok := s.(string); ok && schedule == "" {
						schedule = str
					}
				}
			}
			schID := normalizeID("sched_" + svc.Name + "_" + job.Name)
			g.SchedulerNodes = append(g.SchedulerNodes, &SchedulerNode{
				ID: schID, Name: job.Name, Service: svc.Name,
				Schedule: schedule, Profile: profile,
			})
			g.Edges = append(g.Edges, &GraphEdge{
				From: "sched:" + schID, To: svc.Name, Type: "scheduler",
				Label: schedule,
			})
		}
	}

	sortServices(g.Services)

	return g
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

// loadDiffMindData reads the exposures/dependencies/connections JSON directories
// from a DiffMind run directory.
func loadDiffMindData(diffmindDir string) (exposures, dependencies, connections map[string][]map[string]any) {
	return artifacts.ReadDiffMindFileMaps(diffmindDir)
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

func graphHTTPTarget(item map[string]any) string {
	d := getDetails(item)
	details := getMap(item, "details")
	nestedTarget := getMap(details, "target")
	candidates := []string{
		d["target_service"],
		d["target_ref"],
		getString(item, "instance"),
		getString(nestedTarget, "ref"),
		getString(nestedTarget, "service"),
		d["target_url"],
		d["url_template"],
		d["url"],
		d["base_url"],
	}
	for _, raw := range candidates {
		target := cleanHTTPServiceTarget(raw)
		if target != "" {
			return target
		}
	}
	return "unresolved-http-target"
}

func cleanHTTPServiceTarget(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || isHTTPOperationLabel(raw) || strings.HasPrefix(raw, "/") || LooksLikeHTTPMethodSlug(raw) {
		return ""
	}
	return normalizeServiceName(raw)
}

func graphCacheName(item map[string]any) string {
	d := getDetails(item)
	details := getMap(item, "details")
	target := getMap(details, "target")
	return firstNonEmpty(
		d["cache"],
		d["cache_name"],
		getString(item, "instance"),
		getString(target, "cache"),
		getString(target, "cache_name"),
		d["resource_name"],
		d["key_pattern"],
		getString(item, "name"),
	)
}

func graphDBName(kind string, item map[string]any, d map[string]string, target, metadataDetails map[string]any) string {
	if kind == "dynamodb" {
		return firstKnown(
			d["table"],
			d["table_or_entity"],
			getString(metadataDetails, "table"),
			getString(metadataDetails, "table_or_entity"),
			firstStringFromAnyList(target["tables"]),
			d["resource_name"],
			getString(item, "instance"),
			d["database_name"],
			getString(target, "database"),
			getString(item, "name"),
		)
	}
	return firstKnown(
		d["database_name"],
		getString(target, "database"),
		getString(metadataDetails, "database_name"),
		getString(item, "instance"),
		d["resource_name"],
		d["table_or_entity"],
		d["table"],
		d["entity"],
		getString(item, "name"),
	)
}

func firstStringFromAnyList(v any) string {
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	case []string:
		for _, s := range t {
			if strings.TrimSpace(s) != "" {
				return s
			}
		}
	case string:
		return t
	}
	return ""
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

func firstKnown(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" || strings.EqualFold(v, "unknown") {
			continue
		}
		return v
	}
	return ""
}

func normalizeServiceName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimPrefix(raw, "service.")
	raw = strings.TrimPrefix(raw, "external.")
	raw = strings.ReplaceAll(raw, "_", "-")
	// Strip URL parts
	for _, prefix := range []string{"https://", "http://"} {
		if strings.HasPrefix(raw, prefix) {
			raw = strings.TrimPrefix(raw, prefix)
			// Take the hostname part
			if idx := strings.Index(raw, "/"); idx > 0 {
				raw = raw[:idx]
			}
		}
	}
	// Clean up
	raw = strings.TrimRight(raw, "/")
	for _, suffix := range []string{
		".example.global",
		".example.biz",
		".lead2cash.svc.cluster.local",
		"-default.lead2cash.svc.cluster.local",
		"-default.data",
		".svc.cluster.local",
		".cluster.local",
	} {
		raw = strings.TrimSuffix(raw, suffix)
	}
	return raw
}

func serviceAliasMap(serviceRepoDirs map[string]string) map[string]string {
	aliases := map[string]string{}
	for name, runDir := range serviceRepoDirs {
		for _, alias := range serviceAliases(name) {
			aliases[alias] = name
		}
		for _, alias := range configuredServiceAliasesForRun(name, runDir) {
			for _, variant := range serviceAliases(alias) {
				aliases[variant] = name
			}
		}
	}
	return aliases
}

type graphRepoConfig struct {
	Service struct {
		ID   string `yaml:"id"`
		Name string `yaml:"name"`
	} `yaml:"service"`
	Aliases struct {
		Services map[string][]string `yaml:"services"`
	} `yaml:"aliases"`
}

func configuredServiceAliasesForRun(serviceName, runDir string) []string {
	repoPath := repoPathForRunDir(runDir)
	if repoPath == "" {
		return nil
	}
	data, err := os.ReadFile(artifacts.RepoConfigurationPath(repoPath))
	if err != nil {
		return nil
	}
	var cfg graphRepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	aliases := []string{cfg.Service.ID, cfg.Service.Name}
	for canonical, vals := range cfg.Aliases.Services {
		if !sameServiceIdentity(canonical, serviceName) &&
			!sameServiceIdentity(canonical, cfg.Service.ID) &&
			!sameServiceIdentity(canonical, cfg.Service.Name) {
			continue
		}
		aliases = append(aliases, canonical)
		aliases = append(aliases, vals...)
	}
	return aliases
}

func repoPathForRunDir(runDir string) string {
	if doc, err := artifacts.ReadDiffMind protocol(runDir); err == nil && doc != nil && strings.TrimSpace(doc.Repository.Path) != "" {
		return strings.TrimSpace(doc.Repository.Path)
	}
	data, err := os.ReadFile(filepath.Join(runDir, "run_manifest.json"))
	if err != nil {
		return ""
	}
	var manifest struct {
		RepoPath string `json:"repo_path"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return ""
	}
	return strings.TrimSpace(manifest.RepoPath)
}

func sameServiceIdentity(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	for _, av := range serviceAliases(a) {
		for _, bv := range serviceAliases(b) {
			if av == bv {
				return true
			}
		}
	}
	return false
}

func serviceAliases(name string) []string {
	normalized := normalizeServiceName(name)
	noHyphen := strings.ReplaceAll(normalized, "-", "")
	noUnderscore := strings.ReplaceAll(normalized, "_", "")
	return []string{
		strings.ToLower(normalized),
		strings.ToLower(strings.ReplaceAll(normalized, "_", "-")),
		strings.ToLower(strings.ReplaceAll(normalized, "-", "_")),
		strings.ToLower(noHyphen),
		strings.ToLower(noUnderscore),
	}
}

func canonicalKnownService(known map[string]string, raw string) (string, bool) {
	for _, alias := range serviceAliases(raw) {
		if svc, ok := known[alias]; ok {
			return svc, true
		}
	}
	return "", false
}

func isHTTPOperationLabel(raw string) bool {
	fields := strings.Fields(raw)
	if len(fields) < 2 {
		return false
	}
	switch strings.ToUpper(fields[0]) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func LooksLikeHTTPMethodSlug(raw string) bool {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "service.")
	raw = strings.ReplaceAll(strings.ToLower(raw), "_", "-")
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
		if raw == method || strings.HasPrefix(raw, method+"-") {
			return true
		}
	}
	return false
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

func normalizeCacheKind(raw, name string) string {
	lower := strings.ToLower(raw + " " + name)
	switch {
	case strings.Contains(lower, "redis"):
		return "redis"
	case strings.Contains(lower, "memcache"):
		return "memcached"
	case strings.TrimSpace(raw) == "" || strings.EqualFold(raw, "database"):
		return "cache"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func inferQueueKind(name string, d map[string]string) string {
	lower := strings.ToLower(name + " " + d["kind"] + " " + d["type"] + " " + d["source"] + " " + d["platform"])
	switch {
	case strings.Contains(lower, "dynamodb_stream") || (strings.Contains(lower, "dynamodb") && strings.Contains(lower, "stream")):
		return "dynamodb_stream"
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
