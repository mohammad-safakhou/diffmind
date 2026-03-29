package graph

import (
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

	for _, entry := range b.registry.AllWithArchitecture() {
		if entry.Identity == nil {
			continue
		}
		for _, res := range entry.Identity.Resources {
			key := res.Kind + ":" + res.Identifier
			if resourceUsers[key] == nil {
				resourceUsers[key] = make(map[string]bool)
			}
			resourceUsers[key][entry.Name] = true
		}
	}

	var shared []model.SharedResource
	for key, users := range resourceUsers {
		if len(users) < 2 {
			continue
		}
		parts := splitFirst(key, ":")
		var services []string
		for svc := range users {
			services = append(services, svc)
		}
		shared = append(shared, model.SharedResource{
			Kind:       parts[0],
			Identifier: parts[1],
			Services:   services,
		})
	}
	return shared
}

func splitFirst(s, sep string) [2]string {
	for i := 0; i < len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return [2]string{s[:i], s[i+len(sep):]}
		}
	}
	return [2]string{s, ""}
}
