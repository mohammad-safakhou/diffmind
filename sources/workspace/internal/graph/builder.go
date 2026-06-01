package graph

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/registry"
	"github.com/mohammad-safakhou/diffmind/internal/resolver"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// Builder constructs the cross-service graph.
type Builder struct {
	registry *registry.Registry
	log      *util.Logger
}

// NewBuilder creates a new graph builder.
func NewBuilder(reg *registry.Registry, log *util.Logger) *Builder {
	return &Builder{registry: reg, log: log}
}

// Build constructs the graph from resolution results.
func (b *Builder) Build(resolution *resolver.Resolution) *model.CrossServiceGraph {
	g := &model.CrossServiceGraph{
		Version:     "v1alpha1",
		GeneratedAt: time.Now().UTC(),
	}

	// Build service nodes.
	serviceSet := make(map[string]bool)
	for _, entry := range b.registry.AllWithArchitecture() {
		serviceSet[entry.Name] = true
		node := model.GraphNode{
			ID:       util.ContentHash("service", entry.Name),
			Name:     entry.Name,
			RepoPath: entry.RepoPath,
		}
		if entry.Architecture != nil {
			node.ExposuresCount = len(entry.Architecture.Exposures)
			node.DependencyCount = len(entry.Architecture.Dependencies)
		}
		if entry.Identity != nil {
			node.Identity = *entry.Identity
		} else {
			node.Identity = model.ServiceIdentity{ServiceName: entry.Name}
		}
		g.Services = append(g.Services, node)
	}

	// Build edges from resolved matches.
	if resolution != nil {
		for _, match := range resolution.Matches {
			// Ensure target service is in the graph (might be identity-only).
			if !serviceSet[match.ToService] {
				serviceSet[match.ToService] = true
				g.Services = append(g.Services, model.GraphNode{
					ID:   util.ContentHash("service", match.ToService),
					Name: match.ToService,
					Identity: model.ServiceIdentity{
						ServiceName: match.ToService,
					},
				})
			}

			edge := model.GraphEdge{
				ID:             util.ContentHash("edge", match.FromService, match.ToService, match.DependencyID),
				FromService:    match.FromService,
				ToService:      match.ToService,
				Type:           match.MatchType,
				FromDependency: match.DependencyName,
				Label:          match.Reasoning,
				Confidence:     match.Confidence,
			}
			g.Edges = append(g.Edges, edge)
		}

		// Detect shared resources (databases accessed by multiple services).
		g.SharedResources = b.detectSharedResources()

		// Add unresolved edges.
		g.Unresolved = resolution.Unresolved
	}

	return g
}

func (b *Builder) detectSharedResources() []model.SharedResource {
	// Group database/queue dependencies by identifier.
	resourceUsers := make(map[string]map[string]bool) // resource → set of services
	resourceKinds := make(map[string]string)
	resourceIDs := make(map[string]string)

	addResource := func(kind, identifier, service string) {
		kind = strings.TrimSpace(kind)
		identifier = strings.TrimSpace(identifier)
		if kind == "" || identifier == "" || service == "" || isGenericResourceIdentifier(identifier) {
			return
		}
		key := strings.ToLower(kind + ":" + identifier)
		if resourceUsers[key] == nil {
			resourceUsers[key] = make(map[string]bool)
			resourceKinds[key] = kind
			resourceIDs[key] = identifier
		}
		resourceUsers[key][service] = true
	}

	for _, entry := range b.registry.AllWithArchitecture() {
		if entry.Identity != nil {
			for _, res := range entry.Identity.Resources {
				addResource(res.Kind, res.Identifier, entry.Name)
			}
		}
		if entry.Architecture == nil {
			continue
		}
		for _, exp := range entry.Architecture.Exposures {
			kind, identifier := exposureResource(exp)
			addResource(kind, identifier, entry.Name)
		}
		for _, dep := range entry.Architecture.Dependencies {
			kind, identifier := dependencyResource(dep)
			addResource(kind, identifier, entry.Name)
		}
	}

	var shared []model.SharedResource
	for key, users := range resourceUsers {
		if len(users) < 2 {
			continue
		}
		var services []string
		for svc := range users {
			services = append(services, svc)
		}
		sort.Strings(services)
		shared = append(shared, model.SharedResource{
			Kind:       resourceKinds[key],
			Identifier: resourceIDs[key],
			Services:   services,
		})
	}
	sort.Slice(shared, func(i, j int) bool {
		return shared[i].Kind+":"+shared[i].Identifier < shared[j].Kind+":"+shared[j].Identifier
	})
	return shared
}

func dependencyResource(dep model.Dependency) (string, string) {
	d := dep.Details
	get := func(keys ...string) string {
		for _, key := range keys {
			if v := strings.TrimSpace(fmt.Sprint(d[key])); v != "" && v != "<nil>" {
				return v
			}
		}
		return ""
	}
	switch dep.Type {
	case "db_operation":
		return "database", firstResourceIdentifier(dep.Instance, get("database_name", "database", "datasource", "connection_string", "table_or_entity", "table", "entity"))
	case "cache_operation":
		return "cache", firstResourceIdentifier(dep.Instance, get("cache_name", "namespace", "database_name", "database", "key_pattern"))
	case "queue_publish":
		return "queue", firstResourceIdentifier(dep.Instance, get("queue", "queue_name", "destination", "topic", "queue_url"))
	}
	return "", ""
}

func exposureResource(exp model.Exposure) (string, string) {
	if exp.Type != "queue_consumer" && exp.Type != "stream_consume" {
		return "", ""
	}
	d := exp.Details
	get := func(keys ...string) string {
		for _, key := range keys {
			if v := strings.TrimSpace(fmt.Sprint(d[key])); v != "" && v != "<nil>" {
				return v
			}
		}
		return ""
	}
	return "queue", firstResourceIdentifier(exp.Instance, get("queue", "queue_name", "destination", "topic", "queue_url"))
}

func firstResourceIdentifier(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" && !isGenericResourceIdentifier(v) {
			return v
		}
	}
	return ""
}

func isGenericResourceIdentifier(identifier string) bool {
	switch strings.ToLower(strings.TrimSpace(identifier)) {
	case "", "[]", "{}", "null", "unknown", "database", "cache", "queue", "postgres", "postgresql", "mysql", "redis", "dynamodb", "mongodb":
		return true
	}
	return false
}
