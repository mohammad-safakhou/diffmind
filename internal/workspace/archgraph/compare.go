package archgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Change compares saved facts, not source revisions or inferred causes. Keys
// are semantic tuples; unstable object IDs, layout and checkout paths are not
// identities. Same-name occurrences are compared as multisets, not collapsed.
type Change struct {
	Kind   string   `json:"kind"`
	Key    string   `json:"key"`
	Change string   `json:"change"`
	Fields []string `json:"fields,omitempty"`
	Before any      `json:"before,omitempty"`
	After  any      `json:"after,omitempty"`
}

type comparisonFact struct {
	kind, key string
	value     map[string]any
}

func Compare(ctx context.Context, before, after *ArchGraph) ([]Change, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	left, err := comparisonFacts(ctx, before)
	if err != nil {
		return nil, err
	}
	right, err := comparisonFacts(ctx, after)
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	out := make([]Change, 0)
	for _, key := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a, existsA := left[key]
		b, existsB := right[key]
		switch {
		case !existsA:
			out = append(out, Change{Kind: b.kind, Key: b.key, Change: "added", After: b.value})
		case !existsB:
			out = append(out, Change{Kind: a.kind, Key: a.key, Change: "removed", Before: a.value})
		default:
			fields := changedFields(a.value, b.value)
			if len(fields) > 0 {
				out = append(out, Change{Kind: a.kind, Key: a.key, Change: "modified", Fields: fields, Before: a.value, After: b.value})
			}
		}
	}
	return out, nil
}

func comparisonFacts(ctx context.Context, g *ArchGraph) (map[string]comparisonFact, error) {
	out := map[string]comparisonFact{}
	add := func(kind string, parts []string, value map[string]any) error {
		key := jsonKey(parts)
		id := kind + ":" + key
		if _, ok := out[id]; ok {
			return fmt.Errorf("duplicate %s identity %s in graph", kind, key)
		}
		out[id] = comparisonFact{kind, key, value}
		return nil
	}
	if g == nil {
		return out, nil
	}
	for _, svc := range g.Services {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if svc == nil {
			continue
		}
		if err := add("service", []string{svc.Name}, map[string]any{"name": svc.Name, "known": svc.Known, "team": svc.Team, "component_kind": svc.ComponentKind, "component_type": svc.ComponentType}); err != nil {
			return nil, err
		}
		groups := map[string][]EntitySummary{"http_route": svc.HTTPRoutes, "rpc_endpoint": svc.RPCEndpoints, "queue_consumer": svc.QueueConsumers, "scheduled_job": svc.ScheduledJobs, "webhook": svc.Webhooks, "cli_command": svc.CLICommands, "dependency": svc.Dependencies}
		for category, items := range groups {
			objects := map[string][]any{}
			for _, item := range items {
				key := jsonKey([]string{svc.Name, category, item.Kind, item.Name})
				objects[key] = append(objects[key], objectFact(item))
			}
			for key, values := range objects {
				out["object:"+key] = comparisonFact{"object", key, map[string]any{"occurrences": sortedValues(values)}}
			}
		}
		// Connection identities use endpoint names when available, avoiding
		// generated flow IDs. Expanded DAG content remains evidence, in order.
		connections := map[string][]any{}
		for _, c := range svc.Connections {
			from, to := c.FromName, c.ToName
			if from == "" {
				from = c.FromID
			}
			if to == "" {
				to = c.ToID
			}
			key := jsonKey([]string{svc.Name, from, to, c.FromType, c.ToType})
			connections[key] = append(connections[key], map[string]any{"summary": c.Summary, "kind": c.Kind, "reachability": c.Reachability, "condition": c.Condition, "data_dependencies": c.DataDependencies, "side_effects": c.SideEffects, "nodes": c.Nodes, "edges": c.Edges})
		}
		for key, values := range connections {
			out["flow:"+key] = comparisonFact{"flow", key, map[string]any{"occurrences": sortedValues(values)}}
		}
	}
	for _, r := range g.ResourceNodes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		value := map[string]any{"name": r.Name, "kind": r.Kind, "platform": r.Platform, "owner_service": r.OwnerService, "owner_team": r.OwnerTeam, "details": r.Details, "tables": tableFacts(r.Tables)}
		if err := add("resource", []string{r.GraphID}, value); err != nil {
			return nil, err
		}
	}
	// Older snapshots may have specialized resource lists without ResourceNodes.
	legacyResources := map[string]bool{}
	legacy := func(id string, value map[string]any) error {
		key := jsonKey([]string{id})
		if legacyResources[key] {
			return fmt.Errorf("duplicate resource identity %s in graph", key)
		}
		legacyResources[key] = true
		if _, ok := out["resource:"+key]; !ok {
			out["resource:"+key] = comparisonFact{"resource", key, value}
		}
		return ctx.Err()
	}
	for _, r := range g.QueueNodes {
		if r != nil {
			if err := legacy("queue:"+r.ID, map[string]any{"name": r.Name, "kind": r.Kind, "fifo": r.FIFO}); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range g.DatabaseNodes {
		if r != nil {
			if err := legacy("db:"+r.ID, map[string]any{"name": r.Name, "kind": r.Kind, "host": r.Host, "tables": tableFacts(r.Tables)}); err != nil {
				return nil, err
			}
		}
	}
	for _, r := range g.SchedulerNodes {
		if r != nil {
			if err := legacy("sched:"+r.ID, map[string]any{"name": r.Name, "service": r.Service, "schedule": r.Schedule, "profile": r.Profile}); err != nil {
				return nil, err
			}
		}
	}
	for _, e := range g.ExternalNodes {
		if e != nil {
			if err := add("external", []string{e.Name}, map[string]any{"name": e.Name, "kind": e.Kind}); err != nil {
				return nil, err
			}
		}
	}
	edges := map[string][]any{}
	for _, e := range g.Edges {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e == nil {
			continue
		}
		key := jsonKey([]string{e.From, e.To, e.Type})
		var details []any
		for _, item := range e.Details {
			details = append(details, objectFact(item))
		}
		edges[key] = append(edges[key], map[string]any{"label": e.Label, "confidence": e.Confidence, "details": sortedValues(details)})
	}
	for key, values := range edges {
		out["relationship:"+key] = comparisonFact{"relationship", key, map[string]any{"occurrences": sortedValues(values)}}
	}
	return out, nil
}

func objectFact(item EntitySummary) map[string]any {
	return map[string]any{"kind": item.Kind, "name": item.Name, "summary": item.Summary, "details": item.Details}
}

func tableFacts(tables []DatabaseTable) []any {
	var out []any
	for _, table := range tables {
		var operations []any
		for _, item := range table.Operations {
			operations = append(operations, objectFact(item))
		}
		out = append(out, map[string]any{"name": table.Name, "kind": table.Kind, "operations": sortedValues(operations)})
	}
	return sortedValues(out)
}

func jsonKey(value any) string { body, _ := json.Marshal(value); return string(body) }

func sortedValues(values []any) []any {
	if values == nil {
		return []any{}
	}
	sort.SliceStable(values, func(i, j int) bool { return jsonKey(values[i]) < jsonKey(values[j]) })
	return values
}

func changedFields(a, b map[string]any) []string {
	keys := map[string]bool{}
	for key := range a {
		keys[key] = true
	}
	for key := range b {
		keys[key] = true
	}
	var out []string
	for key := range keys {
		if jsonKey(a[key]) != jsonKey(b[key]) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
