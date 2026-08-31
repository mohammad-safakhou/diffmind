package archgraph

import (
	"sort"
	"strings"
)

type TeamView struct {
	RunID         string          `json:"run_id"`
	Team          string          `json:"team"`
	Scope         string          `json:"scope"`
	Summary       ViewSummary     `json:"summary"`
	Services      []*ServiceNode  `json:"services"`
	ResourceNodes []*ResourceNode `json:"resource_nodes,omitempty"`
	ExternalNodes []*ExternalNode `json:"external_nodes,omitempty"`
	Edges         []*GraphEdge    `json:"edges"`
}

type ServiceView struct {
	RunID             string          `json:"run_id"`
	Service           *ServiceNode    `json:"service"`
	InboundEdges      []*GraphEdge    `json:"inbound_edges"`
	OutboundEdges     []*GraphEdge    `json:"outbound_edges"`
	NeighborServices  []*ServiceNode  `json:"neighbor_services"`
	ResourceNodes     []*ResourceNode `json:"resource_nodes,omitempty"`
	ExternalNodes     []*ExternalNode `json:"external_nodes,omitempty"`
	AvailableTraceIDs []string        `json:"available_trace_ids,omitempty"`
}

type ResourceView struct {
	RunID    string          `json:"run_id"`
	Resource *ResourceNode   `json:"resource"`
	Services []*ServiceNode  `json:"services"`
	Edges    []*GraphEdge    `json:"edges"`
	Tables   []DatabaseTable `json:"tables,omitempty"`
}

type TraceView struct {
	RunID            string              `json:"run_id"`
	Service          string              `json:"service"`
	ObjectID         string              `json:"object_id,omitempty"`
	Status           string              `json:"status"`
	Quality          []string            `json:"quality,omitempty"`
	Segments         []TraceSegment      `json:"segments"`
	Continuations    []TraceContinuation `json:"continuations,omitempty"`
	RelatedEdges     []*GraphEdge        `json:"related_edges,omitempty"`
	DataDependencies []any               `json:"data_dependencies,omitempty"`
}

type TraceSegment struct {
	Service     string              `json:"service"`
	Entrypoint  string              `json:"entrypoint,omitempty"`
	Connections []ConnectionSummary `json:"connections"`
	Nodes       []any               `json:"nodes,omitempty"`
	Edges       []any               `json:"edges,omitempty"`
	Quality     []string            `json:"quality,omitempty"`
}

type TraceContinuation struct {
	FromService string          `json:"from_service"`
	ToService   string          `json:"to_service"`
	EdgeType    string          `json:"edge_type"`
	Label       string          `json:"label,omitempty"`
	Operations  []EntitySummary `json:"operations,omitempty"`
	Status      string          `json:"status"`
}

type ViewSummary struct {
	ServiceCount  int `json:"service_count"`
	ResourceCount int `json:"resource_count"`
	ExternalCount int `json:"external_count"`
	EdgeCount     int `json:"edge_count"`
	Entrypoints   int `json:"entrypoints"`
	Downstream    int `json:"downstream"`
	Traces        int `json:"traces"`
}

func BuildTeamView(g *ArchGraph, team, scope string) (*TeamView, bool) {
	if g == nil {
		return nil, false
	}
	team = strings.TrimSpace(team)
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "team"
	}
	serviceByName := serviceMap(g)
	teamServices := map[string]bool{}
	visibleServices := map[string]bool{}
	for _, svc := range g.Services {
		if strings.EqualFold(firstNonEmpty(svc.Team, "default"), team) {
			teamServices[svc.Name] = true
			visibleServices[svc.Name] = true
		}
	}
	if len(teamServices) == 0 {
		return nil, false
	}
	if scope == "connected" {
		for _, edge := range g.Edges {
			if teamServices[edge.From] {
				if _, ok := serviceByName[edge.To]; ok {
					visibleServices[edge.To] = true
				}
			}
			if teamServices[edge.To] {
				if _, ok := serviceByName[edge.From]; ok {
					visibleServices[edge.From] = true
				}
			}
		}
	}
	nodeIDs := map[string]bool{}
	edges := filterEdgesForServices(g.Edges, visibleServices, nodeIDs)
	services := servicesFromSet(g, visibleServices)
	resources, externals := graphNodesForIDs(g, nodeIDs)
	out := &TeamView{
		RunID:         g.RunID,
		Team:          team,
		Scope:         scope,
		Services:      services,
		ResourceNodes: resources,
		ExternalNodes: externals,
		Edges:         edges,
	}
	out.Summary = summarizeView(services, resources, externals, edges)
	return out, true
}

func BuildServiceView(g *ArchGraph, serviceName string) (*ServiceView, bool) {
	if g == nil {
		return nil, false
	}
	serviceByName := serviceMap(g)
	svc := serviceByName[serviceName]
	if svc == nil {
		return nil, false
	}
	nodeIDs := map[string]bool{}
	neighborNames := map[string]bool{}
	var inbound []*GraphEdge
	var outbound []*GraphEdge
	for _, edge := range g.Edges {
		if edge.From == serviceName {
			outbound = append(outbound, edge)
			nodeIDs[edge.To] = true
			if serviceByName[edge.To] != nil {
				neighborNames[edge.To] = true
			}
		}
		if edge.To == serviceName {
			inbound = append(inbound, edge)
			nodeIDs[edge.From] = true
			if serviceByName[edge.From] != nil {
				neighborNames[edge.From] = true
			}
		}
	}
	resources, externals := graphNodesForIDs(g, nodeIDs)
	traces := make([]string, 0, len(svc.Connections))
	seenTrace := map[string]bool{}
	for _, conn := range svc.Connections {
		id := firstNonEmpty(conn.FlowID, conn.EntrypointID, conn.FromID)
		if id != "" && !seenTrace[id] {
			seenTrace[id] = true
			traces = append(traces, id)
		}
	}
	sort.Strings(traces)
	return &ServiceView{
		RunID:             g.RunID,
		Service:           svc,
		InboundEdges:      sortedEdges(inbound),
		OutboundEdges:     sortedEdges(outbound),
		NeighborServices:  servicesFromSet(g, neighborNames),
		ResourceNodes:     resources,
		ExternalNodes:     externals,
		AvailableTraceIDs: traces,
	}, true
}

func BuildResourceView(g *ArchGraph, id string) (*ResourceView, bool) {
	if g == nil {
		return nil, false
	}
	id = normalizeResourceLookupID(id)
	resources := resourceMap(g)
	resource := resources[id]
	if resource == nil {
		return nil, false
	}
	serviceByName := serviceMap(g)
	serviceNames := map[string]bool{}
	var edges []*GraphEdge
	for _, edge := range g.Edges {
		if edge.From == id || edge.To == id {
			edges = append(edges, edge)
			if serviceByName[edge.From] != nil {
				serviceNames[edge.From] = true
			}
			if serviceByName[edge.To] != nil {
				serviceNames[edge.To] = true
			}
		}
	}
	return &ResourceView{
		RunID:    g.RunID,
		Resource: resource,
		Services: servicesFromSet(g, serviceNames),
		Edges:    sortedEdges(edges),
		Tables:   resource.Tables,
	}, true
}

func BuildTraceView(g *ArchGraph, serviceName, objectID string) (*TraceView, bool) {
	if g == nil {
		return nil, false
	}
	serviceByName := serviceMap(g)
	svc := serviceByName[serviceName]
	if svc == nil {
		return nil, false
	}
	objectID = strings.TrimSpace(objectID)
	matches := matchingConnections(svc, objectID)
	status := "complete"
	var quality []string
	if len(matches) == 0 {
		status = "partial"
		quality = append(quality, "no local DiffMind protocol flow matched the selected object")
	}
	segment := TraceSegment{
		Service:     serviceName,
		Connections: matches,
	}
	if len(matches) > 0 {
		segment.Entrypoint = firstNonEmpty(matches[0].EntrypointID, matches[0].FromID, matches[0].FromName)
		segment.Nodes = anyList(matches[0].Nodes)
		segment.Edges = anyList(matches[0].Edges)
		if len(segment.Nodes) == 0 || len(segment.Edges) == 0 {
			segment.Quality = append(segment.Quality, "local flow has no expanded DAG nodes or edges")
		}
	}
	relatedEdges := traceRelatedEdges(g, serviceName, matches)
	continuations := traceContinuations(g, serviceName, relatedEdges, serviceByName)
	if len(continuations) == 0 {
		hasOutbound := false
		for _, edge := range relatedEdges {
			if edge.From == serviceName && serviceByName[edge.To] != nil {
				hasOutbound = true
				break
			}
		}
		if hasOutbound {
			status = "partial"
			quality = append(quality, "outbound target exists but no continuation details were matched")
		}
	}
	dataDeps := collectTraceDataDependencies(matches)
	if len(dataDeps) == 0 {
		quality = append(quality, "no field-level data dependencies extracted yet")
	}
	return &TraceView{
		RunID:            g.RunID,
		Service:          serviceName,
		ObjectID:         objectID,
		Status:           status,
		Quality:          quality,
		Segments:         []TraceSegment{segment},
		Continuations:    continuations,
		RelatedEdges:     relatedEdges,
		DataDependencies: dataDeps,
	}, true
}

func matchingConnections(svc *ServiceNode, objectID string) []ConnectionSummary {
	if svc == nil {
		return nil
	}
	var out []ConnectionSummary
	want := strings.TrimSpace(objectID)
	wantKey := normalizeLookup(want)
	for _, conn := range svc.Connections {
		if want == "" ||
			conn.FromID == want ||
			conn.ToID == want ||
			conn.EntrypointID == want ||
			conn.FlowID == want ||
			normalizeLookup(conn.FromName) == wantKey ||
			normalizeLookup(conn.ToName) == wantKey {
			out = append(out, conn)
		}
	}
	return out
}

func traceRelatedEdges(g *ArchGraph, serviceName string, matches []ConnectionSummary) []*GraphEdge {
	matchKeys := map[string]bool{}
	for _, conn := range matches {
		for _, raw := range []string{conn.ToID, conn.ToName, conn.Kind, conn.Summary} {
			if key := normalizeLookup(raw); key != "" {
				matchKeys[key] = true
			}
		}
	}
	var out []*GraphEdge
	for _, edge := range g.Edges {
		if edge.From != serviceName && edge.To != serviceName {
			continue
		}
		if len(matchKeys) == 0 {
			out = append(out, edge)
			continue
		}
		if edgeMatchesTrace(edge, matchKeys) {
			out = append(out, edge)
		}
	}
	return sortedEdges(out)
}

func edgeMatchesTrace(edge *GraphEdge, matchKeys map[string]bool) bool {
	for _, detail := range edge.Details {
		for _, raw := range []string{detail.ID, detail.Name, detail.Kind, detail.Summary} {
			if matchKeys[normalizeLookup(raw)] {
				return true
			}
		}
	}
	if matchKeys[normalizeLookup(edge.Label)] || matchKeys[normalizeLookup(edge.Type)] {
		return true
	}
	return false
}

func traceContinuations(g *ArchGraph, serviceName string, edges []*GraphEdge, serviceByName map[string]*ServiceNode) []TraceContinuation {
	var out []TraceContinuation
	for _, edge := range edges {
		if edge.From != serviceName {
			continue
		}
		target := serviceByName[edge.To]
		if target == nil {
			continue
		}
		status := "partial"
		if len(target.HTTPRoutes) > 0 || len(target.RPCEndpoints) > 0 || len(target.QueueConsumers) > 0 {
			status = "matched_known_service"
		}
		out = append(out, TraceContinuation{
			FromService: edge.From,
			ToService:   edge.To,
			EdgeType:    edge.Type,
			Label:       edge.Label,
			Operations:  edge.Details,
			Status:      status,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ToService != out[j].ToService {
			return out[i].ToService < out[j].ToService
		}
		return out[i].EdgeType < out[j].EdgeType
	})
	return out
}

func collectTraceDataDependencies(matches []ConnectionSummary) []any {
	var out []any
	for _, conn := range matches {
		if conn.DataDependencies == nil {
			continue
		}
		switch v := conn.DataDependencies.(type) {
		case []any:
			out = append(out, v...)
		default:
			out = append(out, v)
		}
	}
	return out
}

func filterEdgesForServices(edges []*GraphEdge, visibleServices map[string]bool, nodeIDs map[string]bool) []*GraphEdge {
	var out []*GraphEdge
	for _, edge := range edges {
		fromVisible := visibleServices[edge.From]
		toVisible := visibleServices[edge.To]
		if fromVisible || toVisible {
			out = append(out, edge)
			nodeIDs[edge.From] = true
			nodeIDs[edge.To] = true
		}
	}
	return sortedEdges(out)
}

func graphNodesForIDs(g *ArchGraph, ids map[string]bool) ([]*ResourceNode, []*ExternalNode) {
	resourceByID := resourceMap(g)
	var resources []*ResourceNode
	for id := range ids {
		if n := resourceByID[id]; n != nil {
			resources = append(resources, n)
		}
	}
	sortResources(resources)
	externalByName := map[string]*ExternalNode{}
	for _, node := range g.ExternalNodes {
		externalByName[node.Name] = node
	}
	var externals []*ExternalNode
	for id := range ids {
		if n := externalByName[id]; n != nil {
			externals = append(externals, n)
		}
	}
	sortExternal(externals)
	return resources, externals
}

func servicesFromSet(g *ArchGraph, names map[string]bool) []*ServiceNode {
	var out []*ServiceNode
	for _, svc := range g.Services {
		if names[svc.Name] {
			out = append(out, svc)
		}
	}
	sortServices(out)
	return out
}

func serviceMap(g *ArchGraph) map[string]*ServiceNode {
	out := map[string]*ServiceNode{}
	if g == nil {
		return out
	}
	for _, svc := range g.Services {
		out[svc.Name] = svc
	}
	return out
}

func resourceMap(g *ArchGraph) map[string]*ResourceNode {
	out := map[string]*ResourceNode{}
	if g == nil {
		return out
	}
	for _, n := range g.ResourceNodes {
		if n == nil {
			continue
		}
		graphID := firstNonEmpty(n.GraphID, "resource:"+n.ID)
		out[graphID] = n
		out[n.ID] = n
	}
	for _, n := range g.DatabaseNodes {
		graphID := "db:" + n.ID
		if out[graphID] == nil {
			out[graphID] = &ResourceNode{ID: n.ID, GraphID: graphID, Name: n.Name, Kind: "database", Platform: n.Kind, Tables: n.Tables, OperationCount: n.OperationCount}
		}
	}
	for _, n := range g.QueueNodes {
		graphID := "queue:" + n.ID
		if out[graphID] == nil {
			out[graphID] = &ResourceNode{ID: n.ID, GraphID: graphID, Name: n.Name, Kind: "queue_topic_stream", Platform: n.Kind}
		}
	}
	for _, n := range g.SchedulerNodes {
		graphID := "sched:" + n.ID
		if out[graphID] == nil {
			out[graphID] = &ResourceNode{ID: n.ID, GraphID: graphID, Name: n.Name, Kind: "scheduler", Platform: "cron", OwnerService: n.Service, Details: map[string]any{"schedule": n.Schedule, "profile": n.Profile}}
		}
	}
	return out
}

func normalizeResourceLookupID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if strings.Contains(id, ":") {
		return id
	}
	return "resource:" + id
}

func summarizeView(services []*ServiceNode, resources []*ResourceNode, externals []*ExternalNode, edges []*GraphEdge) ViewSummary {
	out := ViewSummary{
		ServiceCount:  len(services),
		ResourceCount: len(resources),
		ExternalCount: len(externals),
		EdgeCount:     len(edges),
	}
	for _, svc := range services {
		out.Entrypoints += firstPositive(svc.EntrypointCount, len(svc.HTTPRoutes)+len(svc.RPCEndpoints)+len(svc.QueueConsumers)+len(svc.ScheduledJobs)+len(svc.Webhooks)+len(svc.CLICommands))
		out.Downstream += firstPositive(svc.DownstreamCount, len(svc.Dependencies))
		out.Traces += firstPositive(svc.TraceCount, len(svc.Connections))
	}
	return out
}

func sortedEdges(edges []*GraphEdge) []*GraphEdge {
	out := append([]*GraphEdge(nil), edges...)
	sortEdges(out)
	return out
}

func normalizeLookup(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	raw = strings.ReplaceAll(raw, "_", "-")
	raw = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '/' || r == '.' {
			return r
		}
		return ' '
	}, raw)
	return strings.Join(strings.Fields(raw), " ")
}

func anyList(v any) []any {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		return t
	default:
		return []any{t}
	}
}
