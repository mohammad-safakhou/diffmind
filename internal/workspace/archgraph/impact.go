package archgraph

import (
	"fmt"
	"sort"
	"strings"
)

// Impact analysis answers "what is affected if this changes?" — the reverse
// question of a trace. Graph edges point in dependency direction for
// synchronous protocols (caller → callee, service → database), so impact
// travels AGAINST those edges: changing the callee breaks the caller. For
// messaging, data flows with the edge (publisher → queue → consumer), so
// impact travels WITH publish/subscription/consume edges: changing the
// publisher changes what every consumer receives.
var impactReverseTypes = map[string]bool{
	"http": true, "rpc": true, "workflow": true, "database": true, "cache": true,
}

var impactForwardTypes = map[string]bool{
	"queue_publish": true, "queue_subscription": true, "queue_consume": true, "scheduler": true,
}

// BuildImpactView walks the blast radius of a node. target is a service name
// or a resource graph ID ("queue:x", "db:x", "resource:x"). The result reuses
// the FlowView shape so the trace ribbon renders it unchanged; depths are
// hops away from the changed node.
func BuildImpactView(g *ArchGraph, target string, opts FlowOptions) (*FlowView, bool) {
	if g == nil {
		return nil, false
	}
	target = strings.TrimSpace(target)
	if opts.Depth <= 0 {
		opts.Depth = 6
	}
	if opts.MaxNodes <= 0 {
		opts.MaxNodes = 500
	}
	services := serviceMap(g)
	resources := resourceMap(g)
	nodeExists := func(id string) bool {
		if services[id] != nil {
			return true
		}
		if _, ok := resources[id]; ok {
			return true
		}
		for _, e := range g.Edges {
			if e.From == id || e.To == id {
				return true
			}
		}
		return false
	}
	if target == "" || !nodeExists(target) {
		return nil, false
	}

	// impactNeighbors: nodes affected when `id` changes.
	forward := map[string][]*GraphEdge{}
	reverse := map[string][]*GraphEdge{}
	for _, e := range g.Edges {
		if e == nil {
			continue
		}
		if impactForwardTypes[e.Type] {
			forward[e.From] = append(forward[e.From], e)
		}
		if impactReverseTypes[e.Type] {
			reverse[e.To] = append(reverse[e.To], e)
		}
	}

	b := &flowBuilder{
		graph:        g,
		opts:         opts,
		services:     services,
		resources:    resources,
		queues:       queueMapByGraphID(g),
		edgesByFrom:  graphEdgesByFrom(g),
		serviceDepth: map[string]int{},
		serviceEntry: map[string]string{},
		nodes:        map[string]FlowNode{},
		edges:        map[string]FlowEdge{},
		visited:      map[string]bool{},
	}

	type visit struct {
		id    string
		depth int
	}
	rootID := b.addGraphTargetNode(target, 0, "impact_root")
	if svc := services[target]; svc != nil {
		b.addService(svc, 0, "impact_root")
	}
	queue := []visit{{id: target, depth: 0}}
	seen := map[string]bool{target: true}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v.depth >= opts.Depth {
			b.truncated = true
			continue
		}
		if len(b.nodes) >= opts.MaxNodes {
			b.truncated = true
			break
		}
		fromNodeID := b.addGraphTargetNode(v.id, v.depth, "impacted")
		if v.id == target {
			fromNodeID = rootID
		}
		expand := func(next string, edge *GraphEdge) {
			toNodeID := b.addGraphTargetNode(next, v.depth+1, "impacted")
			if svc := services[next]; svc != nil {
				b.addService(svc, v.depth+1, "impacted")
			}
			b.addEdge(FlowEdge{
				From:         fromNodeID,
				To:           toNodeID,
				Kind:         edge.Type,
				Async:        impactForwardTypes[edge.Type] && edge.Type != "scheduler",
				CrossService: services[next] != nil && next != v.id,
				MatchStatus:  "impacted",
			})
			if !seen[next] {
				seen[next] = true
				queue = append(queue, visit{id: next, depth: v.depth + 1})
			}
		}
		for _, e := range forward[v.id] {
			expand(e.To, e)
		}
		for _, e := range reverse[v.id] {
			expand(e.From, e)
		}
	}

	view := b.view(target, "")
	view.Entry = FlowEntry{Service: target}
	if view.Status == "partial" {
		// Impact walks have no Protocol-flow expectations; quality notes from the
		// shared builder don't apply.
		view.Status = "complete"
		if b.truncated {
			view.Status = "truncated"
		}
	}
	view.Quality = nil
	if len(seen) == 1 {
		view.Quality = []string{fmt.Sprintf("nothing in the graph depends on %q yet", target)}
	}
	sort.Slice(view.Services, func(i, j int) bool {
		if view.Services[i].Depth != view.Services[j].Depth {
			return view.Services[i].Depth < view.Services[j].Depth
		}
		return view.Services[i].Name < view.Services[j].Name
	})
	return view, true
}
