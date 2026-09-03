package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/archgraph"
)

var ErrObjectNotFound = errors.New("object not found")
var ErrNodeNotFound = errors.New("graph node not found")

type PathResult struct {
	ProjectID string                 `json:"project_id"`
	RunID     string                 `json:"run_id"`
	From      string                 `json:"from"`
	To        string                 `json:"to"`
	Status    string                 `json:"status"`
	Nodes     []string               `json:"nodes"`
	Edges     []*archgraph.GraphEdge `json:"edges"`
	Depth     int                    `json:"depth"`
	Visited   int                    `json:"visited"`
	Notes     []string               `json:"notes"`
}

// FindPath returns one deterministic shortest directed dependency path. A
// bounded search never claims absence when it has not exhausted the graph.
func (s *Service) FindPath(ctx context.Context, projectID, runID, from, to string, depth int) (*PathResult, error) {
	if depth == 0 {
		depth = 6
	}
	if depth < 1 || depth > 20 {
		return nil, fmt.Errorf("depth must be between 1 and 20")
	}
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" {
		return nil, fmt.Errorf("from and to node IDs are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, g, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	nodes := graphNodeIDs(g)
	if !nodes[from] || !nodes[to] {
		return nil, fmt.Errorf("%w: use exact service names or resource graph IDs", ErrNodeNotFound)
	}
	out := &PathResult{ProjectID: run.ProjectID, RunID: run.ID, From: from, To: to, Status: "not_found", Nodes: []string{}, Edges: []*archgraph.GraphEdge{}, Depth: depth, Visited: 1, Notes: []string{"This is a directed dependency path, not proof of end-to-end execution or request reachability. Edge details retain the saved evidence."}}
	if from == to {
		out.Status = "found"
		out.Nodes = []string{from}
		return out, nil
	}
	adj := map[string][]*archgraph.GraphEdge{}
	const edgeBudget = 100000
	if len(g.Edges) > edgeBudget {
		out.Status = "limited"
		out.Notes = append(out.Notes, "Graph exceeds the 100000-edge search budget; no absence conclusion is possible.")
		return out, nil
	}
	for _, edge := range g.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if edge != nil && nodes[edge.From] && nodes[edge.To] {
			adj[edge.From] = append(adj[edge.From], edge)
		}
	}
	for _, edges := range adj {
		sort.SliceStable(edges, func(i, j int) bool {
			a, b := edges[i], edges[j]
			if a.To != b.To {
				return a.To < b.To
			}
			if a.Type != b.Type {
				return a.Type < b.Type
			}
			if a.Label != b.Label {
				return a.Label < b.Label
			}
			return traceJSON(a) < traceJSON(b)
		})
	}
	type visit struct {
		node  string
		depth int
	}
	queue := []visit{{from, 0}}
	seen := map[string]bool{from: true}
	parents := map[string]*archgraph.GraphEdge{}
	limited := false
	for head := 0; head < len(queue); head++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := queue[head]
		for _, edge := range adj[current.node] {
			if seen[edge.To] {
				continue
			}
			if current.depth >= depth {
				limited = true
				continue
			}
			if len(seen) >= 10000 {
				out.Status = "limited"
				out.Visited = len(seen)
				out.Notes = append(out.Notes, "Stopped at the 10000-node search budget.")
				return out, nil
			}
			seen[edge.To] = true
			parents[edge.To] = edge
			if edge.To == to {
				out.Status = "found"
				out.Visited = len(seen)
				out.Nodes = []string{to}
				for node := to; node != from; {
					e := parents[node]
					out.Edges = append(out.Edges, e)
					out.Nodes = append(out.Nodes, e.From)
					node = e.From
				}
				for i, j := 0, len(out.Nodes)-1; i < j; i, j = i+1, j-1 {
					out.Nodes[i], out.Nodes[j] = out.Nodes[j], out.Nodes[i]
				}
				for i, j := 0, len(out.Edges)-1; i < j; i, j = i+1, j-1 {
					out.Edges[i], out.Edges[j] = out.Edges[j], out.Edges[i]
				}
				return out, nil
			}
			queue = append(queue, visit{edge.To, current.depth + 1})
		}
	}
	out.Visited = len(seen)
	if limited {
		out.Status = "limited"
		out.Notes = append(out.Notes, "The depth limit prevented a complete search; increase depth before concluding there is no path.")
	}
	return out, nil
}

func graphNodeIDs(g *archgraph.ArchGraph) map[string]bool {
	nodes := map[string]bool{}
	for _, s := range g.Services {
		if s != nil {
			nodes[s.Name] = true
		}
	}
	for _, r := range g.ResourceNodes {
		if r != nil {
			nodes[r.GraphID] = true
		}
	}
	for _, r := range g.QueueNodes {
		if r != nil {
			nodes["queue:"+r.ID] = true
		}
	}
	for _, r := range g.DatabaseNodes {
		if r != nil {
			nodes["db:"+r.ID] = true
		}
	}
	for _, r := range g.SchedulerNodes {
		if r != nil {
			nodes["sched:"+r.ID] = true
		}
	}
	for _, r := range g.ExternalNodes {
		if r != nil {
			nodes[r.Name] = true
		}
	}
	delete(nodes, "")
	return nodes
}

type ObjectTrace struct {
	ProjectID       string                        `json:"project_id"`
	RunID           string                        `json:"run_id"`
	Service         string                        `json:"service"`
	ObjectID        string                        `json:"object_id"`
	Status          string                        `json:"status"`
	Objects         []archgraph.EntitySummary     `json:"objects"`
	Connections     []archgraph.ConnectionSummary `json:"connections"`
	RelatedEdges    []*archgraph.GraphEdge        `json:"related_edges"`
	ConnectionCount int                           `json:"connection_count"`
	EdgeCount       int                           `json:"edge_count"`
	Truncated       bool                          `json:"truncated"`
	Notes           []string                      `json:"notes"`
}

// TraceObject deliberately uses exact IDs, not name/label similarity. Service
// adjacency is not enough to attach an edge to the requested object's flow.
func (s *Service) TraceObject(ctx context.Context, projectID, runID, service, objectID string) (*ObjectTrace, error) {
	service, objectID = strings.TrimSpace(service), strings.TrimSpace(objectID)
	if service == "" || objectID == "" {
		return nil, fmt.Errorf("service and exact object_id are required; use get_service to discover IDs")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, g, err := s.Load(projectID, runID)
	if err != nil {
		return nil, err
	}
	var svc *archgraph.ServiceNode
	for _, candidate := range g.Services {
		if candidate != nil && candidate.Name == service {
			svc = candidate
			break
		}
	}
	if svc == nil {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, service)
	}
	out := &ObjectTrace{ProjectID: run.ProjectID, RunID: run.ID, Service: service, ObjectID: objectID, Status: "partial", Objects: []archgraph.EntitySummary{}, Connections: []archgraph.ConnectionSummary{}, RelatedEdges: []*archgraph.GraphEdge{}, Notes: []string{"Only exact saved object/flow IDs are matched. Related dependency edges do not establish cross-service execution continuity."}}
	groups := [][]archgraph.EntitySummary{svc.HTTPRoutes, svc.RPCEndpoints, svc.QueueConsumers, svc.ScheduledJobs, svc.Webhooks, svc.CLICommands, svc.Dependencies}
	for _, items := range groups {
		for _, item := range items {
			if item.ID == objectID {
				out.Objects = append(out.Objects, item)
			}
		}
	}
	dependencyIDs := map[string]bool{objectID: true}
	for _, c := range svc.Connections {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if c.FromID == objectID || c.ToID == objectID || c.FlowID == objectID || c.EntrypointID == objectID {
			out.ConnectionCount++
			out.Connections = append(out.Connections, c)
			if c.ToID != "" {
				dependencyIDs[c.ToID] = true
			}
		}
	}
	if len(out.Objects) == 0 && out.ConnectionCount == 0 {
		return nil, fmt.Errorf("%w: %s", ErrObjectNotFound, objectID)
	}
	for _, edge := range g.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if edge == nil || (edge.From != service && edge.To != service) {
			continue
		}
		// Incoming HTTP/RPC details belong to the caller, not this service.
		if edge.From != service && edge.Type != "queue_consume" && edge.Type != "scheduler" {
			continue
		}
		copy := *edge
		copy.Details = nil
		for _, detail := range edge.Details {
			if detail.ID != "" && dependencyIDs[detail.ID] {
				copy.Details = append(copy.Details, detail)
			}
		}
		if len(copy.Details) > 0 {
			out.EdgeCount++
			out.RelatedEdges = append(out.RelatedEdges, &copy)
		}
	}
	sort.Slice(out.Connections, func(i, j int) bool {
		a, b := out.Connections[i], out.Connections[j]
		if a.FlowID != b.FlowID {
			return a.FlowID < b.FlowID
		}
		if a.FromID != b.FromID {
			return a.FromID < b.FromID
		}
		if a.ToID != b.ToID {
			return a.ToID < b.ToID
		}
		return traceJSON(a) < traceJSON(b)
	})
	sort.Slice(out.RelatedEdges, func(i, j int) bool {
		a, b := out.RelatedEdges[i], out.RelatedEdges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return traceJSON(a) < traceJSON(b)
	})
	// Sort before truncating so the same matches survive artifact reordering.
	out.Connections = out.Connections[:min(200, len(out.Connections))]
	out.RelatedEdges = out.RelatedEdges[:min(200, len(out.RelatedEdges))]
	out.Truncated = out.ConnectionCount > len(out.Connections) || out.EdgeCount > len(out.RelatedEdges)
	if out.ConnectionCount > 0 {
		out.Status = "local_flow_available"
	} else {
		out.Notes = append(out.Notes, "No local flow was extracted for this object; declared dependencies alone do not prove entrypoint reachability.")
	}
	if out.Truncated {
		out.Notes = append(out.Notes, "Response is limited to 200 connections and 200 related edges; counts report the full matches.")
	}
	return out, nil
}

func traceJSON(value any) string { body, _ := json.Marshal(value); return string(body) }
