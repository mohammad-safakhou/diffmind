package archgraph

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type FlowOptions struct {
	Depth    int
	MaxNodes int
	Expand   string
}

type FlowView struct {
	RunID    string        `json:"run_id"`
	Entry    FlowEntry     `json:"entry"`
	Status   string        `json:"status"`
	Quality  []string      `json:"quality,omitempty"`
	Services []FlowService `json:"services"`
	Nodes    []FlowNode    `json:"nodes"`
	Edges    []FlowEdge    `json:"edges"`
	Stats    FlowStats     `json:"stats"`
}

type FlowEntry struct {
	Service  string `json:"service"`
	ObjectID string `json:"object_id,omitempty"`
}

type FlowService struct {
	Name        string `json:"name"`
	Team        string `json:"team,omitempty"`
	Depth       int    `json:"depth"`
	EntryStatus string `json:"entry_status"`
}

type FlowNode struct {
	ID      string         `json:"id"`
	Service string         `json:"service,omitempty"`
	Kind    string         `json:"kind"`
	Label   string         `json:"label"`
	Details map[string]any `json:"details,omitempty"`
}

type FlowEdge struct {
	From         string         `json:"from"`
	To           string         `json:"to"`
	Kind         string         `json:"kind"`
	Async        bool           `json:"async,omitempty"`
	CrossService bool           `json:"cross_service,omitempty"`
	Reachability string         `json:"reachability,omitempty"`
	Condition    map[string]any `json:"condition,omitempty"`
	Cycle        bool           `json:"cycle,omitempty"`
	MatchStatus  string         `json:"match_status,omitempty"`
}

type FlowStats struct {
	ServiceCount int `json:"service_count"`
	NodeCount    int `json:"node_count"`
	EdgeCount    int `json:"edge_count"`
	CycleCount   int `json:"cycle_count"`
	TruncatedAt  int `json:"truncated_at,omitempty"`
}

type flowBuilder struct {
	graph       *ArchGraph
	opts        FlowOptions
	services    map[string]*ServiceNode
	resources   map[string]*ResourceNode
	queues      map[string]*QueueNode
	edgesByFrom map[string][]*GraphEdge

	serviceDepth map[string]int
	serviceEntry map[string]string
	nodes        map[string]FlowNode
	edges        map[string]FlowEdge
	visited      map[string]bool
	quality      []string
	truncated    bool
	cycles       int
}

type flowVisit struct {
	service  string
	objectID string
	depth    int
}

func BuildFlowView(g *ArchGraph, serviceName, objectID string, opts FlowOptions) (*FlowView, bool) {
	if g == nil {
		return nil, false
	}
	serviceName = strings.TrimSpace(serviceName)
	services := serviceMap(g)
	if services[serviceName] == nil {
		return nil, false
	}
	if opts.Depth <= 0 {
		opts.Depth = 6
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 500
	}
	b := &flowBuilder{
		graph:        g,
		opts:         opts,
		services:     services,
		resources:    resourceMap(g),
		queues:       queueMapByGraphID(g),
		edgesByFrom:  graphEdgesByFrom(g),
		serviceDepth: map[string]int{},
		serviceEntry: map[string]string{},
		nodes:        map[string]FlowNode{},
		edges:        map[string]FlowEdge{},
		visited:      map[string]bool{},
	}
	b.walk([]flowVisit{{service: serviceName, objectID: strings.TrimSpace(objectID), depth: 0}})
	return b.view(serviceName, objectID), true
}

func (b *flowBuilder) walk(queue []flowVisit) {
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v.depth > b.opts.Depth {
			b.truncated = true
			continue
		}
		key := v.service + "\x00" + v.objectID
		if b.visited[key] {
			continue
		}
		b.visited[key] = true
		svc := b.services[v.service]
		if svc == nil {
			continue
		}
		b.addService(svc, v.depth, entryStatus(v.objectID))
		serviceNodeID := flowServiceNodeID(svc.Name)
		matches := matchingConnections(svc, v.objectID)
		if len(matches) == 0 {
			b.quality = append(b.quality, fmt.Sprintf("%s: no local DiffMind protocol flow matched %q", svc.Name, v.objectID))
		}
		for _, conn := range matches {
			fromID := b.addObjectNode(svc.Name, conn.FromID, conn.FromName, conn.FromType)
			toID := b.addObjectNode(svc.Name, conn.ToID, conn.ToName, conn.ToType)
			b.addEdge(FlowEdge{
				From:         fromID,
				To:           toID,
				Kind:         firstNonEmpty(conn.Kind, conn.ToType, "local"),
				Reachability: conn.Reachability,
				Condition:    conn.Condition,
				MatchStatus:  "local_flow",
			})
			b.addEdge(FlowEdge{From: serviceNodeID, To: fromID, Kind: "entry", MatchStatus: "selected_entry"})
		}
		related := traceRelatedEdges(b.graph, svc.Name, matches)
		if len(matches) == 0 && v.objectID == "" {
			related = outboundEdges(b.graph, svc.Name)
		}
		for _, edge := range related {
			if edge.From != svc.Name {
				continue
			}
			fromID := b.sourceNodeForEdge(svc.Name, matches, edge)
			queue = b.expandGraphEdge(queue, v, fromID, edge)
		}
	}
}

func (b *flowBuilder) expandGraphEdge(queue []flowVisit, v flowVisit, fromID string, edge *GraphEdge) []flowVisit {
	toID := b.addGraphTargetNode(edge.To, v.depth+1, "matched_graph_edge")
	flowEdge := FlowEdge{
		From:         fromID,
		To:           toID,
		Kind:         edge.Type,
		Async:        edge.Type == "queue_publish" || edge.Type == "queue_consume",
		CrossService: b.services[edge.To] != nil,
		MatchStatus:  "matched_graph_edge",
	}
	if target := b.services[edge.To]; target != nil {
		entryID, status := matchServiceEntrypoint(edge, target)
		flowEdge.MatchStatus = status
		next := flowVisit{service: target.Name, objectID: entryID, depth: v.depth + 1}
		if b.markCycle(next) {
			flowEdge.Cycle = true
		} else if entryID != "" && v.depth < b.opts.Depth {
			queue = append(queue, next)
		}
		b.addEdge(flowEdge)
		return queue
	}
	b.addEdge(flowEdge)
	if strings.HasPrefix(edge.To, "queue:") {
		for _, consume := range b.edgesByFrom[edge.To] {
			if target := b.services[consume.To]; target != nil {
				consumerID, status := matchQueueConsumer(edge.To, target)
				targetNodeID := b.addGraphTargetNode(target.Name, v.depth+1, status)
				nextEdge := FlowEdge{
					From:         toID,
					To:           targetNodeID,
					Kind:         consume.Type,
					Async:        true,
					CrossService: true,
					MatchStatus:  status,
				}
				next := flowVisit{service: target.Name, objectID: consumerID, depth: v.depth + 1}
				if b.markCycle(next) {
					nextEdge.Cycle = true
				} else if consumerID != "" && v.depth < b.opts.Depth {
					queue = append(queue, next)
				}
				b.addEdge(nextEdge)
			}
		}
	}
	return queue
}

func (b *flowBuilder) sourceNodeForEdge(serviceName string, matches []ConnectionSummary, edge *GraphEdge) string {
	for _, detail := range edge.Details {
		for _, conn := range matches {
			if detail.ID != "" && detail.ID == conn.ToID {
				return flowObjectNodeID(serviceName, conn.ToID)
			}
			if normalizeLookup(detail.Name) != "" && normalizeLookup(detail.Name) == normalizeLookup(conn.ToName) {
				return flowObjectNodeID(serviceName, conn.ToID)
			}
		}
	}
	return flowServiceNodeID(serviceName)
}

func (b *flowBuilder) markCycle(v flowVisit) bool {
	key := v.service + "\x00" + v.objectID
	if b.visited[key] {
		b.cycles++
		return true
	}
	return false
}

func (b *flowBuilder) addService(svc *ServiceNode, depth int, status string) {
	if prev, ok := b.serviceDepth[svc.Name]; !ok || depth < prev || depth == prev && flowEntryStatusRank(status) > flowEntryStatusRank(b.serviceEntry[svc.Name]) {
		b.serviceDepth[svc.Name] = depth
		b.serviceEntry[svc.Name] = status
	}
	b.addNode(FlowNode{ID: flowServiceNodeID(svc.Name), Service: svc.Name, Kind: "service", Label: svc.Name})
}

func flowEntryStatusRank(status string) int {
	switch status {
	case "selected_entry", "exact_exposure", "exact_queue_consumer":
		return 3
	case "service_queue_entry", "service_rpc_entry":
		return 2
	case "matched_service", "matched_graph_edge":
		return 1
	default:
		return 0
	}
}

func (b *flowBuilder) addObjectNode(service, id, name, kind string) string {
	id = firstNonEmpty(id, normalizeLookup(name))
	nodeID := flowObjectNodeID(service, id)
	b.addNode(FlowNode{ID: nodeID, Service: service, Kind: firstNonEmpty(kind, "object"), Label: firstNonEmpty(name, id)})
	return nodeID
}

func (b *flowBuilder) addGraphTargetNode(id string, depth int, status string) string {
	if svc := b.services[id]; svc != nil {
		b.addService(svc, depth, firstNonEmpty(status, "matched_service"))
		return flowServiceNodeID(id)
	}
	if res := b.resources[id]; res != nil {
		b.addNode(FlowNode{ID: id, Kind: res.Kind, Label: res.Name, Details: map[string]any{"platform": res.Platform}})
		return id
	}
	if q := b.queues[id]; q != nil {
		b.addNode(FlowNode{ID: id, Kind: "queue", Label: q.Name, Details: map[string]any{"platform": q.Kind, "fifo": q.FIFO}})
		return id
	}
	b.addNode(FlowNode{ID: id, Kind: "external", Label: id})
	return id
}

func (b *flowBuilder) addNode(node FlowNode) {
	if len(b.nodes) >= b.opts.MaxNodes {
		b.truncated = true
		return
	}
	if _, exists := b.nodes[node.ID]; exists {
		return
	}
	b.nodes[node.ID] = node
}

func (b *flowBuilder) addEdge(edge FlowEdge) {
	if edge.From == "" || edge.To == "" {
		return
	}
	key := edge.From + "\x00" + edge.To + "\x00" + edge.Kind + "\x00" + edge.MatchStatus
	if _, exists := b.edges[key]; exists {
		return
	}
	b.edges[key] = edge
}

func (b *flowBuilder) view(serviceName, objectID string) *FlowView {
	services := make([]FlowService, 0, len(b.serviceDepth))
	for name, depth := range b.serviceDepth {
		svc := b.services[name]
		team := ""
		if svc != nil {
			team = svc.Team
		}
		services = append(services, FlowService{Name: name, Team: team, Depth: depth, EntryStatus: firstNonEmpty(b.serviceEntry[name], "matched_service")})
	}
	sort.Slice(services, func(i, j int) bool {
		if services[i].Depth != services[j].Depth {
			return services[i].Depth < services[j].Depth
		}
		return services[i].Name < services[j].Name
	})
	nodes := make([]FlowNode, 0, len(b.nodes))
	for _, node := range b.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	edges := make([]FlowEdge, 0, len(b.edges))
	for _, edge := range b.edges {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		if edges[i].To != edges[j].To {
			return edges[i].To < edges[j].To
		}
		return edges[i].Kind < edges[j].Kind
	})
	status := "complete"
	if b.truncated {
		status = "truncated"
	} else if len(b.quality) > 0 {
		status = "partial"
	}
	view := &FlowView{
		RunID:    b.graph.RunID,
		Entry:    FlowEntry{Service: serviceName, ObjectID: strings.TrimSpace(objectID)},
		Status:   status,
		Quality:  uniqueStrings(b.quality),
		Services: services,
		Nodes:    nodes,
		Edges:    edges,
		Stats: FlowStats{
			ServiceCount: len(services),
			NodeCount:    len(nodes),
			EdgeCount:    len(edges),
			CycleCount:   b.cycles,
		},
	}
	if b.truncated {
		view.Stats.TruncatedAt = b.opts.MaxNodes
	}
	return view
}

func queueMapByGraphID(g *ArchGraph) map[string]*QueueNode {
	out := map[string]*QueueNode{}
	if g == nil {
		return out
	}
	for _, q := range g.QueueNodes {
		if q != nil {
			out["queue:"+q.ID] = q
		}
	}
	return out
}

func graphEdgesByFrom(g *ArchGraph) map[string][]*GraphEdge {
	out := map[string][]*GraphEdge{}
	if g == nil {
		return out
	}
	for _, edge := range g.Edges {
		if edge != nil {
			out[edge.From] = append(out[edge.From], edge)
		}
	}
	for from := range out {
		out[from] = sortedEdges(out[from])
	}
	return out
}

func outboundEdges(g *ArchGraph, serviceName string) []*GraphEdge {
	var out []*GraphEdge
	for _, edge := range g.Edges {
		if edge != nil && edge.From == serviceName {
			out = append(out, edge)
		}
	}
	return sortedEdges(out)
}

func matchServiceEntrypoint(edge *GraphEdge, target *ServiceNode) (string, string) {
	if edge == nil || target == nil {
		return "", "unmatched"
	}
	if edge.Type == "http" {
		method, path := routeFromEdge(edge)
		if id := matchHTTPRoute(method, path, target.HTTPRoutes); id != "" {
			return id, "exact_exposure"
		}
	}
	if edge.Type == "rpc" && len(target.RPCEndpoints) > 0 {
		return target.RPCEndpoints[0].ID, "service_rpc_entry"
	}
	return "", "matched_service"
}

func matchQueueConsumer(queueGraphID string, target *ServiceNode) (string, string) {
	if target == nil || len(target.QueueConsumers) == 0 {
		return "", "matched_service"
	}
	queueKey := strings.TrimPrefix(strings.ToLower(queueGraphID), "queue:")
	for _, consumer := range target.QueueConsumers {
		if queueKey != "" && queueKey == normalizeQueueRoute(firstNonEmpty(detailString(consumer.Details, "queue"), detailString(consumer.Details, "topic"), detailString(consumer.Details, "destination"), consumer.Name)) {
			return consumer.ID, "exact_queue_consumer"
		}
	}
	return target.QueueConsumers[0].ID, "service_queue_entry"
}

func routeFromEdge(edge *GraphEdge) (method, path string) {
	for _, detail := range edge.Details {
		if detail.Details != nil {
			method = strings.ToUpper(firstNonEmpty(detailString(detail.Details, "method"), method))
			for _, key := range []string{"path", "url_template", "endpoint", "url"} {
				if value := detailString(detail.Details, key); value != "" {
					path = routePathOnly(value)
					break
				}
			}
		}
		if path == "" {
			m, p := parseHTTPRouteText(detail.Name)
			method = firstNonEmpty(method, m)
			path = p
		}
		if path != "" {
			return method, path
		}
	}
	return "", ""
}

func matchHTTPRoute(method, path string, routes []EntitySummary) string {
	path = normalizeHTTPRoutePath(path)
	method = strings.ToUpper(strings.TrimSpace(method))
	if path == "" {
		return ""
	}
	var matches []string
	for _, route := range routes {
		rm, rp := routeFromEntity(route)
		if normalizeHTTPRoutePath(rp) != path {
			continue
		}
		if method != "" && rm != "" && method != rm {
			continue
		}
		if route.ID != "" {
			matches = append(matches, route.ID)
		}
	}
	if len(matches) == 1 {
		return matches[0]
	}
	return ""
}

func routeFromEntity(entity EntitySummary) (method, path string) {
	if entity.Details != nil {
		method = strings.ToUpper(detailString(entity.Details, "method"))
		for _, key := range []string{"path", "url_template", "route", "endpoint", "url"} {
			if value := detailString(entity.Details, key); value != "" {
				path = routePathOnly(value)
				break
			}
		}
	}
	if method == "" || path == "" {
		m, p := parseHTTPRouteText(entity.Name)
		method = firstNonEmpty(method, m)
		path = firstNonEmpty(path, p)
	}
	return method, path
}

func parseHTTPRouteText(raw string) (method, path string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	re := regexp.MustCompile(`(?i)\b(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\s+([^\s]+)`)
	if m := re.FindStringSubmatch(raw); len(m) > 2 {
		return strings.ToUpper(m[1]), routePathOnly(m[2])
	}
	return "", ""
}

func routePathOnly(raw string) string {
	raw = strings.TrimSpace(strings.Trim(raw, `"'`))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		parseable := regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(raw, "placeholder")
		if u, err := url.Parse(parseable); err == nil && u.Path != "" {
			raw = u.Path
		}
	}
	if idx := strings.Index(raw, "?"); idx >= 0 {
		raw = raw[:idx]
	}
	if !strings.HasPrefix(raw, "/") {
		return ""
	}
	return raw
}

func normalizeHTTPRoutePath(raw string) string {
	raw = routePathOnly(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = regexp.MustCompile(`\$\{[^}]+\}`).ReplaceAllString(raw, "{}")
	raw = regexp.MustCompile(`\{[^}/]+\}`).ReplaceAllString(raw, "{}")
	raw = regexp.MustCompile(`:[a-z_][a-z0-9_]*`).ReplaceAllString(raw, "{}")
	raw = regexp.MustCompile(`/+`).ReplaceAllString(raw, "/")
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		return "/"
	}
	return raw
}

func normalizeQueueRoute(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(strings.Trim(raw, `"'`)))
	raw = strings.TrimPrefix(raw, "queue:")
	if strings.HasPrefix(raw, "arn:") {
		if idx := strings.LastIndex(raw, ":"); idx >= 0 && idx+1 < len(raw) {
			raw = raw[idx+1:]
		}
	}
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil {
			raw = strings.Trim(strings.TrimSpace(u.Path), "/")
			if idx := strings.LastIndex(raw, "/"); idx >= 0 && idx+1 < len(raw) {
				raw = raw[idx+1:]
			}
		}
	}
	fifo := strings.HasSuffix(raw, ".fifo")
	raw = strings.TrimSuffix(raw, ".fifo")
	raw = regexp.MustCompile(`[-_.]+`).ReplaceAllString(raw, "")
	if fifo {
		raw += ".fifo"
	}
	return raw
}

func flowServiceNodeID(service string) string {
	return "service:" + service
}

func flowObjectNodeID(service, id string) string {
	return "object:" + service + ":" + normalizeLookup(id)
}

func entryStatus(objectID string) string {
	if strings.TrimSpace(objectID) == "" {
		return "all_entries"
	}
	return "selected_entry"
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func detailString(details map[string]any, key string) string {
	if details == nil {
		return ""
	}
	if s, ok := details[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}
