package archgraph

// Architecture graph view. This builds a rich, dependency-typed drill-down
// graph directly from the DiffMind artifacts bound to a run. It is separate
// from graph.json, which is built from service identities and dependency
// targets.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"gopkg.in/yaml.v3"
)

// ArchGraph is the architecture graph for a single run.
type ArchGraph struct {
	RunID          string             `json:"run_id"`
	Layout         *GraphLayout       `json:"layout,omitempty"`
	Services       []*ServiceNode     `json:"services"`
	ExternalNodes  []*ExternalNode    `json:"external_nodes"`
	ResourceNodes  []*ResourceNode    `json:"resource_nodes,omitempty"`
	QueueNodes     []*QueueNode       `json:"queue_nodes"`
	DatabaseNodes  []*DatabaseNode    `json:"database_nodes"`
	SchedulerNodes []*SchedulerNode   `json:"scheduler_nodes"`
	Edges          []*GraphEdge       `json:"edges"`
	Connectivity   *ConnectivityStats `json:"connectivity,omitempty"`
}

// ConnectivityStats is the graph-quality scoreboard persisted with every run
// so regressions in extraction or resolution are visible in the UI, not just
// to whoever remembers to run the report script.
type ConnectivityStats struct {
	Services              int            `json:"services"`
	ServiceToServiceEdges int            `json:"service_to_service_edges"`
	IsolatedServices      int            `json:"isolated_services"`
	AsyncChains           int            `json:"async_chains"`
	EdgesByType           map[string]int `json:"edges_by_type"`
}

// computeConnectivity derives the scoreboard from a built graph. An async
// chain is one producer→consumer service pair connected through a queue,
// directly or across an SNS→SQS subscription hop.
func computeConnectivity(g *ArchGraph) *ConnectivityStats {
	stats := &ConnectivityStats{EdgesByType: map[string]int{}}
	names := map[string]bool{}
	for _, svc := range g.Services {
		if svc != nil {
			names[svc.Name] = true
		}
	}
	stats.Services = len(names)
	touched := map[string]bool{}
	pub := map[string][]string{}
	con := map[string][]string{}
	sub := map[string][]string{}
	for _, e := range g.Edges {
		if e == nil {
			continue
		}
		stats.EdgesByType[e.Type]++
		touched[e.From], touched[e.To] = true, true
		if names[e.From] && names[e.To] {
			stats.ServiceToServiceEdges++
		}
		switch e.Type {
		case "queue_publish":
			pub[e.To] = append(pub[e.To], e.From)
		case "queue_consume":
			con[e.From] = append(con[e.From], e.To)
		case "queue_subscription":
			sub[e.From] = append(sub[e.From], e.To)
		}
	}
	for name := range names {
		if !touched[name] {
			stats.IsolatedServices++
		}
	}
	chains := map[string]bool{}
	for topic, producers := range pub {
		consumers := append([]string{}, con[topic]...)
		for _, q := range sub[topic] {
			consumers = append(consumers, con[q]...)
		}
		for _, p := range producers {
			for _, c := range consumers {
				chains[p+"\x00"+c] = true
			}
		}
	}
	stats.AsyncChains = len(chains)
	return stats
}

type GraphLayout struct {
	Algorithm string       `json:"algorithm"`
	Seed      string       `json:"seed"`
	Nodes     []LayoutNode `json:"nodes"`
}

type LayoutNode struct {
	ID      string  `json:"id"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Rank    int     `json:"rank"`
	Cluster string  `json:"cluster,omitempty"`
	Role    string  `json:"role,omitempty"`
}

type SchedulerNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Service  string `json:"service"`
	Schedule string `json:"schedule"`
	Profile  string `json:"profile,omitempty"`
}

type ResourceNode struct {
	ID             string          `json:"id"`
	GraphID        string          `json:"graph_id"`
	Name           string          `json:"name"`
	Kind           string          `json:"kind"` // database, cache, object_storage, queue_topic_stream, scheduler, workflow, saas_api
	Platform       string          `json:"platform,omitempty"`
	OwnerService   string          `json:"owner_service,omitempty"`
	OwnerTeam      string          `json:"owner_team,omitempty"`
	Tables         []DatabaseTable `json:"tables,omitempty"`
	OperationCount int             `json:"operation_count,omitempty"`
	Details        map[string]any  `json:"details,omitempty"`
}

type ServiceNode struct {
	Name              string              `json:"name"`
	Known             bool                `json:"known"`
	RepoID            string              `json:"repo_id,omitempty"`
	RepoPath          string              `json:"repo_path,omitempty"`
	Team              string              `json:"team,omitempty"`
	ComponentKind     string              `json:"component_kind,omitempty"`
	ComponentType     string              `json:"component_type,omitempty"`
	DiffMindFreshness string              `json:"diffmind_freshness,omitempty"`
	RepoMetrics       *model.RepoMetrics  `json:"repo_metrics,omitempty"`
	HTTPRoutes        []EntitySummary     `json:"http_routes"`
	RPCEndpoints      []EntitySummary     `json:"rpc_endpoints"`
	QueueConsumers    []EntitySummary     `json:"queue_consumers"`
	ScheduledJobs     []EntitySummary     `json:"scheduled_jobs"`
	Webhooks          []EntitySummary     `json:"webhooks"`
	CLICommands       []EntitySummary     `json:"cli_commands"`
	Databases         []string            `json:"databases"`
	Dependencies      []EntitySummary     `json:"dependencies"`
	Connections       []ConnectionSummary `json:"connections"`
	EntrypointCount   int                 `json:"entrypoint_count,omitempty"`
	DownstreamCount   int                 `json:"downstream_count,omitempty"`
	TraceCount        int                 `json:"trace_count,omitempty"`
}

type ConnectionSummary struct {
	FromID           string         `json:"from_id,omitempty"`
	FromName         string         `json:"from_name"`
	FromType         string         `json:"from_type"`
	ToID             string         `json:"to_id,omitempty"`
	ToName           string         `json:"to_name"`
	ToType           string         `json:"to_type"`
	FlowID           string         `json:"flow_id,omitempty"`
	EntrypointID     string         `json:"entrypoint_id,omitempty"`
	Summary          string         `json:"summary"`
	Kind             string         `json:"kind,omitempty"`
	Reachability     string         `json:"reachability,omitempty"`
	Condition        map[string]any `json:"condition,omitempty"`
	DataDependencies any            `json:"data_dependencies,omitempty"`
	SideEffects      any            `json:"side_effects,omitempty"`
	Nodes            any            `json:"nodes,omitempty"`
	Edges            any            `json:"edges,omitempty"`
}

type ExternalNode struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "service", "api", "saas", "workflow"
}

type QueueNode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"` // "sqs", "sns", "kafka", "kinesis"
	FIFO bool   `json:"fifo"`
}

type DatabaseNode struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Kind           string          `json:"kind"` // "postgresql", "dynamodb", "athena", "redis", "elasticsearch"
	Host           string          `json:"host,omitempty"`
	Tables         []DatabaseTable `json:"tables,omitempty"`
	OperationCount int             `json:"operation_count,omitempty"`
}

type DatabaseTable struct {
	Name       string          `json:"name"`
	Kind       string          `json:"kind,omitempty"`
	Operations []EntitySummary `json:"operations,omitempty"`
}

type GraphEdge struct {
	From       string          `json:"from"`
	FromPort   string          `json:"from_port"`
	To         string          `json:"to"`
	ToPort     string          `json:"to_port"`
	Type       string          `json:"type"` // "http", "rpc", "workflow", "queue_publish", "queue_consume", "database", "cache", "scheduler"
	Label      string          `json:"label"`
	Details    []EntitySummary `json:"details"`
	Confidence float64         `json:"confidence"`
}

type EntitySummary struct {
	ID      string         `json:"id,omitempty"`
	Kind    string         `json:"kind,omitempty"`
	Name    string         `json:"name"`
	Summary string         `json:"summary"`
	Details map[string]any `json:"details,omitempty"`
}

// Overview returns a graph suitable for first paint. It keeps topology,
// counts, teams, and resource summaries, but strips object/evidence detail that
// can be lazy-loaded when the user enters full-detail or service/object views.
func Overview(g *ArchGraph) *ArchGraph {
	if g == nil {
		return nil
	}
	out := &ArchGraph{
		RunID:          g.RunID,
		Layout:         g.Layout,
		Connectivity:   g.Connectivity,
		Services:       make([]*ServiceNode, 0, len(g.Services)),
		ExternalNodes:  g.ExternalNodes,
		ResourceNodes:  make([]*ResourceNode, 0, len(g.ResourceNodes)),
		QueueNodes:     g.QueueNodes,
		DatabaseNodes:  make([]*DatabaseNode, 0, len(g.DatabaseNodes)),
		SchedulerNodes: g.SchedulerNodes,
		Edges:          make([]*GraphEdge, 0, len(g.Edges)),
	}
	for _, svc := range g.Services {
		if svc == nil {
			continue
		}
		out.Services = append(out.Services, &ServiceNode{
			Name:              svc.Name,
			Known:             svc.Known,
			RepoID:            svc.RepoID,
			RepoPath:          svc.RepoPath,
			Team:              svc.Team,
			ComponentKind:     svc.ComponentKind,
			ComponentType:     svc.ComponentType,
			DiffMindFreshness: svc.DiffMindFreshness,
			RepoMetrics:       svc.RepoMetrics,
			EntrypointCount:   firstPositive(svc.EntrypointCount, len(svc.HTTPRoutes)+len(svc.RPCEndpoints)+len(svc.QueueConsumers)+len(svc.ScheduledJobs)+len(svc.Webhooks)+len(svc.CLICommands)),
			DownstreamCount:   firstPositive(svc.DownstreamCount, len(svc.Dependencies)),
			TraceCount:        firstPositive(svc.TraceCount, len(svc.Connections)),
		})
	}
	for _, node := range g.ResourceNodes {
		out.ResourceNodes = append(out.ResourceNodes, overviewResourceNode(node))
	}
	for _, node := range g.DatabaseNodes {
		out.DatabaseNodes = append(out.DatabaseNodes, overviewDatabaseNode(node))
	}
	for _, edge := range g.Edges {
		if edge == nil {
			continue
		}
		out.Edges = append(out.Edges, &GraphEdge{
			From:       edge.From,
			FromPort:   edge.FromPort,
			To:         edge.To,
			ToPort:     edge.ToPort,
			Type:       edge.Type,
			Label:      edge.Label,
			Confidence: edge.Confidence,
		})
	}
	return out
}

func overviewResourceNode(node *ResourceNode) *ResourceNode {
	if node == nil {
		return nil
	}
	out := *node
	out.Details = nil
	out.Tables = overviewTables(node.Tables)
	return &out
}

func overviewDatabaseNode(node *DatabaseNode) *DatabaseNode {
	if node == nil {
		return nil
	}
	out := *node
	out.Tables = overviewTables(node.Tables)
	return &out
}

func overviewTables(tables []DatabaseTable) []DatabaseTable {
	if len(tables) == 0 {
		return nil
	}
	out := make([]DatabaseTable, 0, len(tables))
	for _, table := range tables {
		out = append(out, DatabaseTable{Name: table.Name, Kind: table.Kind})
	}
	return out
}

func firstPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// Build builds the architecture graph from DiffMind artifacts.
// serviceRepoDirs maps a service name to the DiffMind run directory whose
// exposures/dependencies/connections describe that service.
func Build(runID string, serviceRepoDirs map[string]string) *ArchGraph {
	return BuildWithSupplements(runID, serviceRepoDirs, nil)
}

// BuildWithSupplements uses the same graph builder for pack fixtures, the UI,
// and MCP. Supplements do not mutate extractor artifacts.
func BuildWithSupplements(runID string, serviceRepoDirs map[string]string, supplements map[string]Supplement) *ArchGraph {
	g := &ArchGraph{RunID: runID}

	knownServices := serviceAliasMap(serviceRepoDirs)
	allOutboundHTTP := map[string][]outboundRef{} // source svc -> targets
	allOutboundRPC := map[string][]outboundRef{}  // source svc -> targets
	allWorkflow := map[string][]outboundRef{}     // source svc -> workflow engines
	allQueuePublish := map[string][]queueRef{}    // source svc -> queues
	allQueueConsume := map[string][]queueRef{}    // source svc -> queues
	allDBs := map[string][]dbRef{}                // source svc -> databases
	allCacheOps := map[string][]dbRef{}           // source svc -> caches

	// Phase 1: Load all DiffMind data per service
	for _, name := range sortedStringKeys(serviceRepoDirs) {
		diffmindDir := serviceRepoDirs[name]
		exposures, dependencies, connections := loadDiffMindData(diffmindDir)
		if exposures == nil {
			exposures = map[string][]map[string]any{}
		}
		if dependencies == nil {
			dependencies = map[string][]map[string]any{}
		}
		supplementData(exposures, dependencies, supplements[name])

		svc := &ServiceNode{
			Name:  name,
			Known: true,
		}
		meta := serviceMetadataForRun(name, diffmindDir)
		svc.RepoPath = meta.repoPath
		svc.Team = meta.team
		svc.ComponentKind = meta.componentKind
		svc.ComponentType = meta.componentType
		svc.RepoMetrics = meta.repoMetrics

		// Extract exposures
		for _, item := range exposures["http_route"] {
			svc.HTTPRoutes = append(svc.HTTPRoutes, toSummary(item))
		}
		for _, item := range exposures["rpc_endpoint"] {
			svc.RPCEndpoints = append(svc.RPCEndpoints, toSummary(item))
		}
		for _, item := range exposures["queue_consumer"] {
			svc.QueueConsumers = append(svc.QueueConsumers, toSummary(item))
			d := getDetails(item)
			qName := firstNonEmpty(d["stream_arn"], d["event_source_arn"], d["source"], d["destination"], d["topic"], d["queue"], d["queue_name"], getString(item, "instance"), getString(item, "name"))
			qDisplay, qKey := canonicalQueueName(qName)
			if qKey == "" {
				continue
			}
			qKey = scopedQueueKey(qKey, name, d)
			kind := inferQueueKind(qName, d)
			fifo := strings.Contains(strings.ToLower(qName), "fifo")
			allQueueConsume[name] = append(allQueueConsume[name], queueRef{name: qDisplay, key: qKey, kind: kind, fifo: fifo, summary: toSummary(item)})
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
			// Keep refs without a resolvable target: they get a second chance
			// via cross-service route matching once all services are loaded.
			allOutboundHTTP[name] = append(allOutboundHTTP[name], outboundRef{
				target:    graphServiceTarget(item),
				endpoints: toSummary(item),
			})
		}
		for _, item := range dependencies["outbound_rpc"] {
			target := graphServiceTarget(item)
			if target == "" {
				continue
			}
			allOutboundRPC[name] = append(allOutboundRPC[name], outboundRef{
				target:    target,
				endpoints: toSummary(item),
			})
		}
		for _, item := range dependencies["workflow_orchestration"] {
			target := graphWorkflowTarget(item)
			if target == "" {
				target = "camunda"
			}
			allWorkflow[name] = append(allWorkflow[name], outboundRef{
				target:    target,
				endpoints: toSummary(item),
			})
		}
		for _, item := range dependencies["queue_publish"] {
			d := getDetails(item)
			qName := firstNonEmpty(d["stream_arn"], d["event_source_arn"], d["source"], d["destination"], d["topic"], d["queue"], d["queue_name"], getString(item, "instance"), getString(item, "name"))
			qDisplay, qKey := canonicalQueueName(qName)
			if qKey == "" {
				continue
			}
			qKey = scopedQueueKey(qKey, name, d)
			kind := inferQueueKind(qName, d)
			fifo := strings.Contains(strings.ToLower(qName), "fifo")
			allQueuePublish[name] = append(allQueuePublish[name], queueRef{name: qDisplay, key: qKey, kind: kind, fifo: fifo, summary: toSummary(item)})
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
			dbName, tableName := graphDBResource(dbKind, name, item, d, target, metadataDetails)
			host := firstNonEmpty(d["host_production"], d["host"])
			// Extract operations list for edge labels
			op := extractOperations(item)
			allDBs[name] = append(allDBs[name], dbRef{name: dbName, table: tableName, kind: dbKind, operation: op, host: host, summary: toSummary(item)})
			svc.Databases = append(svc.Databases, firstNonEmpty(tableName, dbName))
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
		for _, depType := range []string{"outbound_http", "outbound_rpc", "workflow_orchestration", "queue_publish", "db_operation", "cache_operation"} {
			for _, item := range dependencies[depType] {
				svc.Dependencies = append(svc.Dependencies, toSummary(item))
			}
		}

		// Build connection summaries by matching IDs
		expByID := map[string]string{}
		for _, expType := range []string{"http_route", "rpc_endpoint", "queue_consumer", "scheduled_job", "webhook", "cli_command"} {
			for _, item := range exposures[expType] {
				if id := getString(item, "id"); id != "" {
					expByID[id] = getString(item, "name")
				}
			}
		}
		depByID := map[string]string{}
		for _, depType := range []string{"outbound_http", "outbound_rpc", "workflow_orchestration", "queue_publish", "db_operation", "cache_operation"} {
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
				details := getMap(c, "details")
				fromName := expByID[fromID]
				toName := depByID[toID]
				if fromName == "" {
					fromName = fromID
				}
				if toName == "" {
					toName = toID
				}
				svc.Connections = append(svc.Connections, ConnectionSummary{
					FromID:   fromID,
					FromName: fromName,
					FromType: getString(c, "from_type"),
					ToID:     toID,
					ToName:   toName,
					ToType:   getString(c, "to_type"),
					FlowID:   getString(c, "id"),
					EntrypointID: firstNonEmpty(
						getString(details, "entrypoint"),
						fromID,
					),
					Summary: getString(c, "summary"),
					Kind:    firstNonEmpty(getString(details, "kind"), getString(c, "summary")),
					Reachability: firstNonEmpty(
						getString(details, "reachability"),
						getString(getMap(c, "condition"), "kind"),
					),
					Condition:        getMap(details, "condition"),
					DataDependencies: details["data_dependencies"],
					SideEffects:      details["side_effects"],
					Nodes:            details["nodes"],
					Edges:            details["edges"],
				})
			}
		}

		svc.EntrypointCount = len(svc.HTTPRoutes) + len(svc.RPCEndpoints) + len(svc.QueueConsumers) + len(svc.ScheduledJobs) + len(svc.Webhooks) + len(svc.CLICommands)
		svc.DownstreamCount = len(svc.Dependencies)
		svc.TraceCount = len(svc.Connections)
		g.Services = append(g.Services, svc)
	}

	// Phase 2: Discover external services, queues, databases
	externalSvcs := map[string]*ExternalNode{}
	queueMap := map[string]*QueueNode{}
	dbMap := map[string]*DatabaseNode{}
	resourceMap := map[string]*ResourceNode{}
	resourceEdges := map[string]*GraphEdge{}
	httpEdges := map[string]*GraphEdge{}
	rpcEdges := map[string]*GraphEdge{}
	workflowEdges := map[string]*GraphEdge{}

	// Databases
	for _, svcName := range sortedStringKeys(allDBs) {
		dbs := allDBs[svcName]
		sortDBRefs(dbs)
		for _, db := range dbs {
			category := graphResourceKindForDBKind(db.kind)
			if category != "database" {
				resourceID, resourceName, childName := graphResourceNodeIdentity(category, db.kind, svcName, db)
				graphID := "resource:" + resourceID
				if _, ok := resourceMap[graphID]; !ok {
					resourceMap[graphID] = &ResourceNode{ID: resourceID, GraphID: graphID, Name: resourceName, Kind: category, Platform: db.kind}
				}
				addResourceOperation(resourceMap[graphID], childName, db.summary)
				addResourceEdge(resourceEdges, svcName, graphID, category, db.operation, db.summary)
				continue
			}
			dbID, dbName, tableName := graphDBNodeIdentity(svcName, db)
			graphID := "db:" + dbID
			if _, ok := dbMap[dbID]; !ok {
				dbMap[dbID] = &DatabaseNode{ID: dbID, Name: dbName, Kind: db.kind, Host: db.host}
				resourceMap[graphID] = &ResourceNode{ID: dbID, GraphID: graphID, Name: dbName, Kind: "database", Platform: db.kind}
			}
			addDatabaseOperation(dbMap[dbID], tableName, db.summary)
			addResourceOperation(resourceMap[graphID], tableName, db.summary)
			addResourceEdge(resourceEdges, svcName, graphID, "database", db.operation, db.summary)
		}
	}

	// Caches
	for _, svcName := range sortedStringKeys(allCacheOps) {
		ops := allCacheOps[svcName]
		sortDBRefs(ops)
		for _, op := range ops {
			resourceID, resourceName, childName := graphResourceNodeIdentity("cache", op.kind, svcName, op)
			graphID := "resource:" + resourceID
			if _, ok := resourceMap[graphID]; !ok {
				resourceMap[graphID] = &ResourceNode{ID: resourceID, GraphID: graphID, Name: resourceName, Kind: "cache", Platform: op.kind}
			}
			addResourceOperation(resourceMap[graphID], childName, op.summary)
			addResourceEdge(resourceEdges, svcName, graphID, "cache", op.operation, op.summary)
		}
	}

	for _, key := range sortedStringKeys(resourceEdges) {
		edge := resourceEdges[key]
		edge.Label = resourceEdgeLabel(edge)
		g.Edges = append(g.Edges, edge)
	}

	// Queues - publish
	queueEdges := map[string]*GraphEdge{}
	for _, svcName := range sortedStringKeys(allQueuePublish) {
		pubs := allQueuePublish[svcName]
		sortQueueRefs(pubs)
		for _, q := range pubs {
			qID := q.key
			if _, ok := queueMap[qID]; !ok {
				queueMap[qID] = &QueueNode{ID: qID, Name: q.name, Kind: q.kind, FIFO: q.fifo}
				graphID := "queue:" + qID
				resourceMap[graphID] = &ResourceNode{ID: qID, GraphID: graphID, Name: q.name, Kind: "queue_topic_stream", Platform: q.kind}
			}
			addTargetEdge(queueEdges, svcName, "queue:"+qID, "queue_publish", "publish", q.summary)
		}
	}

	// Queues - consume
	for _, svcName := range sortedStringKeys(allQueueConsume) {
		cons := allQueueConsume[svcName]
		sortQueueRefs(cons)
		for _, q := range cons {
			qID := q.key
			if _, ok := queueMap[qID]; !ok {
				queueMap[qID] = &QueueNode{ID: qID, Name: q.name, Kind: q.kind, FIFO: q.fifo}
				graphID := "queue:" + qID
				resourceMap[graphID] = &ResourceNode{ID: qID, GraphID: graphID, Name: q.name, Kind: "queue_topic_stream", Platform: q.kind}
			}
			addTargetEdge(queueEdges, "queue:"+qID, svcName, "queue_consume", "consume", q.summary)
		}
	}
	for _, key := range sortedStringKeys(queueEdges) {
		g.Edges = append(g.Edges, queueEdges[key])
	}

	// Queue fan-out topology from infra repos: SNS topic → SQS queue
	// subscriptions live in terraform, not in any service's code, and are the
	// only thing that can connect a publisher's topic to consumer queues.
	subscriptionEdges := map[string]bool{}
	for _, svc := range g.Services {
		if svc.RepoPath == "" {
			continue
		}
		for _, sub := range ScanTerraformSubscriptions(svc.RepoPath) {
			topicDisplay, topicKey := canonicalQueueName(sub.Topic)
			queueDisplay, queueKey := canonicalQueueName(sub.Queue)
			if topicKey == "" || queueKey == "" || topicKey == queueKey {
				continue
			}
			// Only materialize links that touch a queue some service actually
			// publishes to or consumes from; pure infra-only pairs would fill
			// the graph with dangling nodes.
			_, topicKnown := queueMap[topicKey]
			_, queueKnown := queueMap[queueKey]
			if !topicKnown && !queueKnown {
				continue
			}
			ensureQueueNode := func(key, display, kind string) {
				if _, ok := queueMap[key]; ok {
					return
				}
				queueMap[key] = &QueueNode{ID: key, Name: display, Kind: kind}
				graphID := "queue:" + key
				resourceMap[graphID] = &ResourceNode{ID: key, GraphID: graphID, Name: display, Kind: "queue_topic_stream", Platform: kind}
			}
			ensureQueueNode(topicKey, topicDisplay, "sns")
			ensureQueueNode(queueKey, queueDisplay, "sqs")
			edgeKey := topicKey + "|" + queueKey
			if subscriptionEdges[edgeKey] {
				continue
			}
			subscriptionEdges[edgeKey] = true
			g.Edges = append(g.Edges, &GraphEdge{
				From: "queue:" + topicKey, To: "queue:" + queueKey, Type: "queue_subscription",
				Label: "sns→sqs",
				Details: []EntitySummary{{
					Name:    sub.Topic + " → " + sub.Queue,
					Details: map[string]any{"source": sub.Source, "match_basis": "terraform_subscription"},
				}},
			})
		}
	}

	// Outbound HTTP
	routeIndex := buildRouteIndex(g.Services)
	for _, svcName := range sortedStringKeys(allOutboundHTTP) {
		targets := allOutboundHTTP[svcName]
		sortOutboundRefs(targets)
		for _, t := range targets {
			targetName := t.target
			summary := t.endpoints
			matched := false
			if targetName != "" {
				if canonical, ok := canonicalKnownService(knownServices, targetName); ok {
					targetName = canonical
					matched = true
				}
			}
			if !matched {
				// Second chance: the outbound call's METHOD+path uniquely
				// identifies one known service's exposed route.
				if owner, ok := matchRouteOwner(routeIndex, svcName, t.endpoints); ok {
					targetName = owner.service
					summary = withMatchDetails(summary, "route", owner.exposureID)
					matched = true
				}
			}
			if !matched {
				if targetName == "" {
					continue
				}
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
			addTargetEdge(httpEdges, svcName, targetName, "http", "HTTP", summary)
		}
	}
	for _, key := range sortedStringKeys(httpEdges) {
		g.Edges = append(g.Edges, httpEdges[key])
	}

	// Outbound RPC/gRPC
	for _, svcName := range sortedStringKeys(allOutboundRPC) {
		targets := allOutboundRPC[svcName]
		sortOutboundRefs(targets)
		for _, t := range targets {
			targetName := t.target
			if canonical, ok := canonicalKnownService(knownServices, targetName); ok {
				targetName = canonical
			} else {
				if _, ok := externalSvcs[targetName]; !ok {
					externalSvcs[targetName] = &ExternalNode{Name: targetName, Kind: "rpc"}
				}
			}
			label := strings.ToUpper(firstNonEmpty(getString(t.endpoints.Details, "protocol"), getString(t.endpoints.Details, "platform"), "rpc"))
			if strings.EqualFold(label, "grpc") {
				label = "gRPC"
			}
			addTargetEdge(rpcEdges, svcName, targetName, "rpc", label, t.endpoints)
		}
	}
	for _, key := range sortedStringKeys(rpcEdges) {
		g.Edges = append(g.Edges, rpcEdges[key])
	}

	// Workflow orchestration engines such as Camunda external-task workers.
	for _, svcName := range sortedStringKeys(allWorkflow) {
		targets := allWorkflow[svcName]
		sortOutboundRefs(targets)
		for _, t := range targets {
			targetName := t.target
			if canonical, ok := canonicalKnownService(knownServices, targetName); ok {
				targetName = canonical
			} else if _, ok := externalSvcs[targetName]; !ok {
				externalSvcs[targetName] = &ExternalNode{Name: targetName, Kind: "workflow"}
			}
			label := strings.ToUpper(firstNonEmpty(getString(t.endpoints.Details, "orchestrator"), getString(t.endpoints.Details, "platform"), "workflow"))
			addTargetEdge(workflowEdges, svcName, targetName, "workflow", label, t.endpoints)
		}
	}
	for _, key := range sortedStringKeys(workflowEdges) {
		g.Edges = append(g.Edges, workflowEdges[key])
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
			graphID := "sched:" + schID
			resourceMap[graphID] = &ResourceNode{
				ID: schID, GraphID: graphID, Name: job.Name, Kind: "scheduler", Platform: "cron",
				OwnerService: svc.Name,
				OwnerTeam:    firstNonEmpty(svc.Team, "default"),
				Details:      map[string]any{"schedule": schedule, "profile": profile},
			}
			g.Edges = append(g.Edges, &GraphEdge{
				From: "sched:" + schID, To: svc.Name, Type: "scheduler",
				Label: schedule,
			})
		}
	}

	sortSchedulers(g.SchedulerNodes)
	for _, id := range sortedStringKeys(resourceMap) {
		g.ResourceNodes = append(g.ResourceNodes, resourceMap[id])
	}
	sortResources(g.ResourceNodes)
	sortServices(g.Services)
	sortEdges(g.Edges)
	g.Layout = buildLayout(g)
	g.Connectivity = computeConnectivity(g)

	return g
}

// ---- Internal Types ----

type outboundRef struct {
	target    string
	endpoints EntitySummary
}

type queueRef struct {
	name    string
	key     string
	kind    string
	fifo    bool
	summary EntitySummary
}

type dbRef struct {
	name      string
	table     string
	kind      string
	operation string
	host      string
	summary   EntitySummary
}

type serviceRunMetadata struct {
	team          string
	repoPath      string
	componentKind string
	componentType string
	repoMetrics   *model.RepoMetrics
}

// ---- Data Loading ----

// loadDiffMindData reads the exposures/dependencies/connections JSON directories
// from a DiffMind run directory.
func loadDiffMindData(diffmindDir string) (exposures, dependencies, connections map[string][]map[string]any) {
	return artifacts.ReadDiffMindFileMaps(diffmindDir)
}

func serviceMetadataForRun(serviceName, runDir string) serviceRunMetadata {
	meta := serviceRunMetadata{}
	if doc, err := artifacts.ReadProtocol(runDir); err == nil && doc != nil {
		meta.team = firstNonEmpty(doc.Service.Team, meta.team)
		meta.repoPath = firstNonEmpty(doc.Repository.Path, meta.repoPath)
	}
	data, err := os.ReadFile(filepath.Join(runDir, "run_manifest.json"))
	if err != nil {
		meta.team = firstNonEmpty(meta.team, "default")
		meta.componentKind, meta.componentType = catalogComponent(meta.repoPath)
		return meta
	}
	var manifest model.RunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		meta.team = firstNonEmpty(meta.team, "default")
		meta.componentKind, meta.componentType = catalogComponent(meta.repoPath)
		return meta
	}
	meta.team = firstNonEmpty(meta.team, manifest.Team, "default")
	meta.repoPath = firstNonEmpty(meta.repoPath, manifest.RepoPath)
	meta.repoMetrics = manifest.RepoMetrics
	kind, typ := catalogComponent(meta.repoPath)
	meta.componentKind = kind
	meta.componentType = typ
	if meta.team == "" && strings.TrimSpace(serviceName) != "" {
		meta.team = "default"
	}
	return meta
}

type catalogInfo struct {
	Kind string `yaml:"kind"`
	Spec struct {
		Type string `yaml:"type"`
	} `yaml:"spec"`
}

func catalogComponent(repoPath string) (kind, typ string) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(repoPath, "catalog-info.yaml"))
	if err != nil {
		return "", ""
	}
	var catalog catalogInfo
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return "", ""
	}
	return strings.TrimSpace(catalog.Kind), strings.TrimSpace(catalog.Spec.Type)
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
	details := getMap(item, "details")
	kind := getString(item, "type")
	if nestedKind := getString(details, "kind"); nestedKind != "" {
		kind = nestedKind
	}
	return EntitySummary{
		ID:      getString(item, "id"),
		Kind:    kind,
		Name:    getString(item, "name"),
		Summary: getString(item, "summary"),
		Details: details,
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

type routeOwner struct {
	service    string
	exposureID string
	method     string
}

// buildRouteIndex maps every known service's exposed HTTP routes by
// normalized path so outbound calls without a resolvable host can still be
// anchored to the one service exposing that route.
func buildRouteIndex(services []*ServiceNode) map[string][]routeOwner {
	idx := map[string][]routeOwner{}
	for _, svc := range services {
		if svc == nil || !svc.Known {
			continue
		}
		for _, route := range svc.HTTPRoutes {
			method, path := routeFromEntity(route)
			norm := normalizeHTTPRoutePath(path)
			if norm == "" || norm == "/" || genericRoutePath(norm) {
				continue
			}
			idx[norm] = append(idx[norm], routeOwner{
				service:    svc.Name,
				exposureID: route.ID,
				method:     strings.ToUpper(strings.TrimSpace(method)),
			})
		}
	}
	return idx
}

// matchRouteOwner resolves an outbound HTTP dependency to the single known
// service exposing the same METHOD+path. Routes owned by more than one
// service stay unmatched: a wrong deterministic edge is worse than a missing
// one.
func matchRouteOwner(idx map[string][]routeOwner, fromService string, dep EntitySummary) (routeOwner, bool) {
	method, path := routeFromEntity(dep)
	norm := normalizeHTTPRoutePath(path)
	if norm == "" || norm == "/" {
		return routeOwner{}, false
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	var match routeOwner
	found := false
	for _, owner := range idx[norm] {
		if owner.service == fromService {
			continue
		}
		if method != "" && owner.method != "" && owner.method != method {
			continue
		}
		if !found {
			match, found = owner, true
			continue
		}
		if owner.service != match.service {
			return routeOwner{}, false
		}
		if owner.exposureID != "" && (match.exposureID == "" || owner.exposureID < match.exposureID) {
			match = owner
		}
	}
	return match, found
}

// genericRoutePath reports whether a normalized path is a well-known generic
// endpoint (auth, health, docs) that many services expose. Such paths cannot
// safely identify a target service even when only one owner declared them.
func genericRoutePath(norm string) bool {
	switch norm {
	case "/oauth/token", "/oauth/authorize", "/token", "/login", "/logout",
		"/health", "/healthz", "/livez", "/readyz", "/status", "/info",
		"/metrics", "/version", "/ping", "/api-docs", "/error":
		return true
	}
	return strings.HasPrefix(norm, "/actuator") || strings.HasPrefix(norm, "/swagger") ||
		strings.HasPrefix(norm, "/health/") || strings.HasPrefix(norm, "/.well-known")
}

func withMatchDetails(summary EntitySummary, basis, exposureID string) EntitySummary {
	details := make(map[string]any, len(summary.Details)+2)
	for k, v := range summary.Details {
		details[k] = v
	}
	details["match_basis"] = basis
	if exposureID != "" {
		details["matched_exposure_id"] = exposureID
	}
	summary.Details = details
	return summary
}

func graphServiceTarget(item map[string]any) string {
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
		d["service"],
		d["service_name"],
	}
	for _, raw := range candidates {
		target := cleanServiceTarget(raw)
		if target != "" {
			return target
		}
	}
	return ""
}

func graphWorkflowTarget(item map[string]any) string {
	d := getDetails(item)
	for _, raw := range []string{
		d["target_service"],
		d["workflow_engine"],
		d["engine"],
		getString(item, "instance"),
		d["url_template"],
		d["base_url"],
		d["value"],
		d["orchestrator"],
	} {
		target := cleanServiceTarget(raw)
		if target != "" {
			return target
		}
	}
	return ""
}

func cleanServiceTarget(raw string) string {
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

func graphDBResource(kind, serviceName string, item map[string]any, d map[string]string, target, metadataDetails map[string]any) (resourceName, tableName string) {
	tableName = graphDBTableName(item, d, target, metadataDetails)
	if isDatasourceDBKind(kind) {
		resourceName = firstKnown(
			d["database_name"],
			getString(target, "database"),
			getString(metadataDetails, "database_name"),
			d["datasource"],
			d["data_source"],
			d["db_name"],
			d["host_production"],
			d["host"],
		)
		if resourceName == "" {
			resourceName = serviceName + "-db"
		}
		return resourceName, tableName
	}
	if kind == "dynamodb" {
		table := firstKnown(tableName, d["resource_name"], getString(item, "instance"), d["database_name"], getString(target, "database"), getString(item, "name"))
		return table, table
	}
	resourceName = firstKnown(
		d["database_name"],
		getString(target, "database"),
		getString(metadataDetails, "database_name"),
		d["resource_name"],
		tableName,
		getString(item, "instance"),
		getString(item, "name"),
	)
	return resourceName, tableName
}

func graphDBTableName(item map[string]any, d map[string]string, target, metadataDetails map[string]any) string {
	return firstKnown(
		d["table"],
		d["table_or_entity"],
		d["entity"],
		getString(metadataDetails, "table"),
		getString(metadataDetails, "table_or_entity"),
		firstStringFromAnyList(target["tables"]),
		d["resource_name"],
		getString(item, "instance"),
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

func graphDBNodeIdentity(serviceName string, db dbRef) (id, name, table string) {
	name = firstNonEmpty(db.name, serviceName+"-db")
	table = firstNonEmpty(db.table, db.name)
	if isDatasourceDBKind(db.kind) {
		id = normalizeID(db.kind + "_" + name)
		return id, name, table
	}
	id = normalizeID(db.kind + "_" + name)
	return id, name, table
}

func graphResourceKindForDBKind(kind string) string {
	switch strings.ToLower(kind) {
	case "redis", "memcached", "cache":
		return "cache"
	case "s3", "s3bucket", "object_storage", "object-storage", "bucket":
		return "object_storage"
	default:
		return "database"
	}
}

func graphResourceNodeIdentity(category, platform, serviceName string, ref dbRef) (id, name, child string) {
	platform = firstNonEmpty(platform, category)
	name = firstNonEmpty(ref.name, serviceName+"-"+category)
	child = firstNonEmpty(ref.table, ref.name)
	if isGenericResourceInstanceName(category, platform, name) {
		switch category {
		case "cache":
			name = serviceName + "-cache"
		case "object_storage":
			name = serviceName + "-object-storage"
		default:
			name = serviceName + "-" + category
		}
	}
	if isGenericResourceInstanceName(category, platform, child) {
		child = firstNonEmpty(ref.operation, name)
	}
	id = normalizeID(platform + "_" + name)
	return id, name, child
}

func isGenericResourceInstanceName(category, platform, name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	lower = strings.Trim(lower, "`\"' ")
	if lower == "" || lower == "unknown" || lower == "database" {
		return true
	}
	switch lower {
	case "getbucket", "gets3bucket", "getbucketname", "put", "get", "set", "delete", "retry", "getfromcache", "puttocache":
		return true
	}
	if category == "object_storage" || strings.Contains(strings.ToLower(platform), "s3") {
		if lower == "s3" || lower == "bucket" {
			return true
		}
		return strings.HasPrefix(lower, "get") && strings.Contains(lower, "bucket")
	}
	if category == "cache" || strings.Contains(strings.ToLower(platform), "redis") {
		if lower == "cache" {
			return true
		}
		switch {
		case strings.HasPrefix(lower, "getfrom"),
			strings.HasPrefix(lower, "putto"),
			strings.HasPrefix(lower, "set"),
			strings.HasPrefix(lower, "delete"),
			strings.HasPrefix(lower, "evict"):
			return true
		}
	}
	return false
}

func isDatasourceDBKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "postgresql", "postgres", "mysql", "mariadb", "athena", "redshift", "bigquery", "snowflake", "oracle", "mssql", "sqlserver", "database":
		return true
	default:
		return false
	}
}

func addDatabaseOperation(node *DatabaseNode, tableName string, summary EntitySummary) {
	if node == nil {
		return
	}
	tableName = firstNonEmpty(tableName, node.Name)
	for i := range node.Tables {
		if node.Tables[i].Name == tableName {
			node.Tables[i].Operations = append(node.Tables[i].Operations, summary)
			node.OperationCount++
			return
		}
	}
	node.Tables = append(node.Tables, DatabaseTable{
		Name:       tableName,
		Kind:       node.Kind,
		Operations: []EntitySummary{summary},
	})
	node.OperationCount++
	sort.Slice(node.Tables, func(i, j int) bool { return node.Tables[i].Name < node.Tables[j].Name })
}

func addResourceOperation(node *ResourceNode, childName string, summary EntitySummary) {
	if node == nil {
		return
	}
	childName = firstNonEmpty(childName, node.Name)
	for i := range node.Tables {
		if node.Tables[i].Name == childName {
			node.Tables[i].Operations = append(node.Tables[i].Operations, summary)
			node.OperationCount++
			return
		}
	}
	node.Tables = append(node.Tables, DatabaseTable{
		Name:       childName,
		Kind:       node.Platform,
		Operations: []EntitySummary{summary},
	})
	node.OperationCount++
	sort.Slice(node.Tables, func(i, j int) bool { return node.Tables[i].Name < node.Tables[j].Name })
}

func addResourceEdge(edges map[string]*GraphEdge, from, to, edgeType, op string, summary EntitySummary) {
	key := from + "|" + to + "|" + edgeType
	if _, ok := edges[key]; !ok {
		edges[key] = &GraphEdge{From: from, To: to, Type: edgeType}
	}
	edge := edges[key]
	edge.Details = append(edge.Details, summary)
	if edge.Label == "" {
		edge.Label = op
	} else if op != "" && !strings.Contains(","+edge.Label+",", ","+op+",") {
		edge.Label += "," + op
	}
}

func addTargetEdge(edges map[string]*GraphEdge, from, to, edgeType, label string, summary EntitySummary) {
	key := from + "|" + to + "|" + edgeType
	if _, ok := edges[key]; !ok {
		edges[key] = &GraphEdge{From: from, To: to, Type: edgeType, Label: label, Confidence: 1.0}
	}
	edges[key].Details = append(edges[key].Details, summary)
	if confidence, ok := summary.Details["detection_confidence"].(float64); ok && confidence < edges[key].Confidence {
		edges[key].Confidence = confidence
	}
}

func resourceEdgeLabel(edge *GraphEdge) string {
	if edge == nil {
		return ""
	}
	count := len(edge.Details)
	if count == 0 {
		return edge.Label
	}
	directions := map[string]int{}
	for _, detail := range edge.Details {
		op := ""
		if detail.Details != nil {
			if v, ok := detail.Details["operation"].(string); ok {
				op = v
			}
			if op == "" {
				if vals, ok := detail.Details["operations"].([]any); ok {
					parts := make([]string, 0, len(vals))
					for _, val := range vals {
						if s, ok := val.(string); ok {
							parts = append(parts, s)
						}
					}
					op = strings.Join(parts, ",")
				}
			}
		}
		dir := layoutOperationDirection(firstNonEmpty(op, edge.Label))
		if dir == "" {
			dir = "op"
		}
		directions[dir]++
	}
	if len(directions) == 1 {
		for dir := range directions {
			if count == 1 {
				return firstNonEmpty(edge.Label, dir)
			}
			if dir == "op" {
				return fmt.Sprintf("%d ops", count)
			}
			return fmt.Sprintf("%d %s ops", count, dir)
		}
	}
	return fmt.Sprintf("%d ops", count)
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
	if doc, err := artifacts.ReadProtocol(runDir); err == nil && doc != nil && strings.TrimSpace(doc.Repository.Path) != "" {
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
	candidates := []string{
		normalized,
		strings.ReplaceAll(normalized, "_", "-"),
		strings.ReplaceAll(normalized, "-", "_"),
		strings.ReplaceAll(normalized, ".", "-"),
		strings.ReplaceAll(normalized, ".", "_"),
		strings.ReplaceAll(strings.ReplaceAll(normalized, ".", "-"), "_", "-"),
		strings.ReplaceAll(strings.ReplaceAll(normalized, ".", "_"), "-", "_"),
	}
	for _, candidate := range append([]string{}, candidates...) {
		candidates = append(candidates,
			strings.ReplaceAll(candidate, "-", ""),
			strings.ReplaceAll(candidate, "_", ""),
			strings.ReplaceAll(candidate, ".", ""),
		)
		if stripped, ok := strings.CutPrefix(candidate, "cdp-"); ok {
			candidates = append(candidates, stripped)
		}
		if stripped, ok := strings.CutPrefix(candidate, "cdp_"); ok {
			candidates = append(candidates, stripped)
		}
		if stripped, ok := strings.CutPrefix(candidate, "cdp."); ok {
			candidates = append(candidates, stripped)
		}
		if alt := catalogueSpellingVariant(candidate); alt != "" {
			candidates = append(candidates, alt)
		}
		if strings.HasSuffix(candidate, "-api") {
			candidates = append(candidates, strings.TrimSuffix(candidate, "-api"))
		} else if candidate != "" {
			candidates = append(candidates, candidate+"-api")
		}
	}
	aliases := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		aliases = append(aliases, candidate)
	}
	return aliases
}

func catalogueSpellingVariant(raw string) string {
	switch {
	case strings.Contains(raw, "catalogue"):
		return strings.ReplaceAll(raw, "catalogue", "catalog")
	case strings.Contains(raw, "catalog"):
		return strings.ReplaceAll(raw, "catalog", "catalogue")
	default:
		return ""
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
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "ANY":
		return true
	default:
		return false
	}
}

func LooksLikeHTTPMethodSlug(raw string) bool {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "service.")
	raw = strings.ReplaceAll(strings.ToLower(raw), "_", "-")
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options", "any"} {
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

// scopedQueueKey prevents false cross-service joins on fallback queue names.
// DiffMind marks each destination's provenance: real infrastructure names
// (literal, resolved config, helm env value) are safe shared identities;
// config-key fallbacks and generic tokens ("queue", "sqs", "queueurl") are
// per-service display names that would otherwise merge unrelated services
// onto one queue node.
func scopedQueueKey(qKey, serviceName string, d map[string]string) string {
	src := d["destination_source"]
	if src == "config_key" || src == "raw" || genericQueueToken(qKey) {
		scoped, _ := canonicalQueueName(serviceName)
		if scoped == "" {
			scoped = serviceName
		}
		return normalizeID(scoped) + ":" + qKey
	}
	return qKey
}

func genericQueueToken(qKey string) bool {
	switch qKey {
	case "sqs", "sns", "kafka", "queue", "queues", "topic", "topics",
		"queueurl", "queuename", "queueurls", "requesturl", "responseurl",
		"sqsevent", "sqsmessage", "sqsmessagebody", "message", "messages",
		"event", "events", "stream", "input", "output", "source", "destination":
		return true
	}
	return len(qKey) < 5
}

func canonicalQueueName(raw string) (display, key string) {
	display = strings.ToLower(strings.TrimSpace(strings.Trim(raw, `"'`)))
	if display == "" {
		return "", ""
	}
	if strings.HasPrefix(display, "queue:") {
		display = strings.TrimSpace(strings.TrimPrefix(display, "queue:"))
	}
	if strings.HasPrefix(display, "arn:") {
		if idx := strings.LastIndex(display, ":"); idx >= 0 && idx+1 < len(display) {
			display = display[idx+1:]
		}
	}
	if strings.Contains(display, "://") {
		if u, err := url.Parse(display); err == nil {
			path := strings.Trim(strings.TrimSpace(u.Path), "/")
			if idx := strings.LastIndex(path, "/"); idx >= 0 && idx+1 < len(path) {
				path = path[idx+1:]
			}
			if path != "" {
				display = path
			}
		}
	}
	fifo := strings.HasSuffix(display, ".fifo")
	base := strings.TrimSuffix(display, ".fifo")
	key = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_' || r == '.':
			return -1
		default:
			return -1
		}
	}, base)
	if fifo {
		key += "_fifo"
	}
	if key == "" {
		return "", ""
	}
	return display, key
}

func normalizeDBKind(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "postgres"):
		return "postgresql"
	case strings.Contains(lower, "s3") || strings.Contains(lower, "bucket"):
		return "s3"
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
func sortResources(s []*ResourceNode) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Kind != s[j].Kind {
			return s[i].Kind < s[j].Kind
		}
		if s[i].Name != s[j].Name {
			return s[i].Name < s[j].Name
		}
		return s[i].GraphID < s[j].GraphID
	})
}
func sortSchedulers(s []*SchedulerNode) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Service != s[j].Service {
			return s[i].Service < s[j].Service
		}
		return s[i].Name < s[j].Name
	})
}

func sortEdges(s []*GraphEdge) {
	sort.Slice(s, func(i, j int) bool {
		a, b := s[i], s[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Label < b.Label
	})
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortDBRefs(items []dbRef) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].kind != items[j].kind {
			return items[i].kind < items[j].kind
		}
		if items[i].name != items[j].name {
			return items[i].name < items[j].name
		}
		return items[i].operation < items[j].operation
	})
}

func sortQueueRefs(items []queueRef) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].kind != items[j].kind {
			return items[i].kind < items[j].kind
		}
		if items[i].key != items[j].key {
			return items[i].key < items[j].key
		}
		return items[i].name < items[j].name
	})
}

func sortOutboundRefs(items []outboundRef) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].target != items[j].target {
			return items[i].target < items[j].target
		}
		return items[i].endpoints.Name < items[j].endpoints.Name
	})
}

type layoutConstraint struct {
	from string
	to   string
}

func buildLayout(g *ArchGraph) *GraphLayout {
	nodeIDs := graphNodeIDs(g)
	if len(nodeIDs) == 0 {
		return &GraphLayout{Algorithm: "protocol-layered-v1", Seed: layoutSeed(g)}
	}
	nodeSet := make(map[string]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeSet[id] = struct{}{}
	}
	constraints := layoutConstraints(g, nodeSet)
	ranks := layoutRanks(nodeIDs, constraints)
	ordered := layoutOrder(nodeIDs, ranks, constraints)

	const rankSep = 460.0
	const rowSep = 170.0
	const x0 = 190.0
	const y0 = 130.0
	out := &GraphLayout{
		Algorithm: "protocol-layered-v1",
		Seed:      layoutSeed(g),
		Nodes:     make([]LayoutNode, 0, len(nodeIDs)),
	}
	for rank := 0; rank < len(ordered); rank++ {
		nodes := ordered[rank]
		for row, id := range nodes {
			w, h := layoutSize(g, id)
			out.Nodes = append(out.Nodes, LayoutNode{
				ID:      id,
				X:       x0 + float64(rank)*rankSep,
				Y:       y0 + float64(row)*rowSep,
				Width:   w,
				Height:  h,
				Rank:    rank,
				Cluster: layoutCluster(g, id),
				Role:    layoutRole(id),
			})
		}
	}
	return out
}

func graphNodeIDs(g *ArchGraph) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, n := range g.Services {
		add(n.Name)
	}
	for _, n := range g.ExternalNodes {
		add(n.Name)
	}
	for _, n := range g.ResourceNodes {
		add(firstNonEmpty(n.GraphID, "resource:"+n.ID))
	}
	for _, n := range g.QueueNodes {
		add("queue:" + n.ID)
	}
	for _, n := range g.DatabaseNodes {
		add("db:" + n.ID)
	}
	for _, n := range g.SchedulerNodes {
		add("sched:" + n.ID)
	}
	sort.Strings(ids)
	return ids
}

func layoutConstraints(g *ArchGraph, nodeSet map[string]struct{}) []layoutConstraint {
	var out []layoutConstraint
	adj := map[string]map[string]struct{}{}
	add := func(from, to string) {
		if from == "" || to == "" || from == to {
			return
		}
		if _, ok := nodeSet[from]; !ok {
			return
		}
		if _, ok := nodeSet[to]; !ok {
			return
		}
		if reaches(adj, to, from) {
			return
		}
		if adj[from] == nil {
			adj[from] = map[string]struct{}{}
		}
		if _, exists := adj[from][to]; exists {
			return
		}
		adj[from][to] = struct{}{}
		out = append(out, layoutConstraint{from: from, to: to})
	}
	for _, e := range g.Edges {
		switch e.Type {
		case "queue_consume", "queue_publish", "http", "scheduler":
			add(e.From, e.To)
		case "database", "cache", "object_storage":
			switch layoutOperationDirection(e.Label) {
			case "read":
				add(e.To, e.From)
			case "write":
				add(e.From, e.To)
			default:
				add(e.From, e.To)
			}
		default:
			add(e.From, e.To)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].from != out[j].from {
			return out[i].from < out[j].from
		}
		return out[i].to < out[j].to
	})
	return out
}

func reaches(adj map[string]map[string]struct{}, from, to string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for next := range adj[n] {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				stack = append(stack, next)
			}
		}
	}
	return false
}

func layoutOperationDirection(label string) string {
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "insert"),
		strings.Contains(lower, "create"),
		strings.Contains(lower, "write"),
		strings.Contains(lower, "put"),
		strings.Contains(lower, "store"),
		strings.Contains(lower, "update"),
		strings.Contains(lower, "delete"),
		strings.Contains(lower, "upsert"),
		strings.Contains(lower, "evict"),
		strings.Contains(lower, "publish"):
		return "write"
	case strings.Contains(lower, "read"),
		strings.Contains(lower, "select"),
		strings.Contains(lower, "query"),
		strings.Contains(lower, "scan"),
		strings.Contains(lower, "get"),
		strings.Contains(lower, "find"),
		strings.Contains(lower, "load"):
		return "read"
	default:
		return ""
	}
}

func layoutRanks(nodeIDs []string, constraints []layoutConstraint) map[string]int {
	rank := make(map[string]int, len(nodeIDs))
	for _, id := range nodeIDs {
		rank[id] = 0
	}
	for i := 0; i < len(nodeIDs); i++ {
		changed := false
		for _, c := range constraints {
			next := rank[c.from] + 1
			if rank[c.to] < next {
				rank[c.to] = next
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return rank
}

func layoutOrder(nodeIDs []string, ranks map[string]int, constraints []layoutConstraint) [][]string {
	maxRank := 0
	for _, r := range ranks {
		if r > maxRank {
			maxRank = r
		}
	}
	byRank := make([][]string, maxRank+1)
	for _, id := range nodeIDs {
		r := ranks[id]
		byRank[r] = append(byRank[r], id)
	}
	for i := range byRank {
		sort.Strings(byRank[i])
	}

	position := func() map[string]int {
		pos := map[string]int{}
		for _, nodes := range byRank {
			for i, id := range nodes {
				pos[id] = i
			}
		}
		return pos
	}
	for pass := 0; pass < 4; pass++ {
		pos := position()
		for r := 1; r < len(byRank); r++ {
			sortByBarycenter(byRank[r], constraints, pos, true)
		}
		pos = position()
		for r := len(byRank) - 2; r >= 0; r-- {
			sortByBarycenter(byRank[r], constraints, pos, false)
		}
	}
	return byRank
}

func sortByBarycenter(nodes []string, constraints []layoutConstraint, pos map[string]int, useIncoming bool) {
	if len(nodes) < 2 {
		return
	}
	score := map[string]float64{}
	for _, id := range nodes {
		var total float64
		var count float64
		for _, c := range constraints {
			var other string
			if useIncoming && c.to == id {
				other = c.from
			}
			if !useIncoming && c.from == id {
				other = c.to
			}
			if other == "" {
				continue
			}
			if p, ok := pos[other]; ok {
				total += float64(p)
				count++
			}
		}
		if count == 0 {
			score[id] = float64(pos[id])
		} else {
			score[id] = total / count
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		if score[nodes[i]] == score[nodes[j]] {
			return nodes[i] < nodes[j]
		}
		return score[nodes[i]] < score[nodes[j]]
	})
}

func layoutSize(g *ArchGraph, id string) (float64, float64) {
	switch {
	case strings.HasPrefix(id, "db:"), strings.HasPrefix(id, "queue:"), strings.HasPrefix(id, "resource:"):
		return 220, 88
	case strings.HasPrefix(id, "sched:"):
		return 190, 72
	case isServiceNode(g, id):
		return 300, 116
	default:
		return 220, 86
	}
}

func isServiceNode(g *ArchGraph, id string) bool {
	for _, svc := range g.Services {
		if svc.Name == id {
			return true
		}
	}
	return false
}

func layoutCluster(g *ArchGraph, id string) string {
	for _, svc := range g.Services {
		if svc.Name == id {
			return firstNonEmpty(svc.Team, "default")
		}
	}
	return layoutRole(id)
}

func layoutRole(id string) string {
	switch {
	case strings.HasPrefix(id, "resource:"):
		return "resource.instance"
	case strings.HasPrefix(id, "db:"):
		return "resource.database"
	case strings.HasPrefix(id, "queue:"):
		return "resource.queue"
	case strings.HasPrefix(id, "sched:"):
		return "activation.scheduler"
	default:
		return "service"
	}
}

func layoutSeed(g *ArchGraph) string {
	parts := []string{g.RunID}
	for _, n := range g.Services {
		parts = append(parts, "svc:"+n.Name)
	}
	for _, n := range g.ExternalNodes {
		parts = append(parts, "ext:"+n.Name)
	}
	for _, n := range g.ResourceNodes {
		parts = append(parts, "resource:"+firstNonEmpty(n.GraphID, n.ID)+":"+n.Kind+":"+n.Platform)
	}
	for _, n := range g.QueueNodes {
		parts = append(parts, "queue:"+n.ID)
	}
	for _, n := range g.DatabaseNodes {
		parts = append(parts, "db:"+n.ID)
	}
	for _, n := range g.SchedulerNodes {
		parts = append(parts, "sched:"+n.ID)
	}
	for _, e := range g.Edges {
		parts = append(parts, e.From+">"+e.To+":"+e.Type+":"+e.Label)
	}
	return strings.Join(parts, "|")
}
