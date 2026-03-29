package graph

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/registry"
	"github.com/mohammad-safakhou/diffmind/internal/resolver"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

func TestBuild_BasicGraph(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("order-service", &model.ServiceArchitecture{
		ServiceName: "order-service",
		RepoPath:    "/repos/order-service",
		Exposures:   []model.Exposure{{BaseEntity: model.BaseEntity{ID: "e1"}}},
		Dependencies: []model.Dependency{
			{BaseEntity: model.BaseEntity{ID: "d1", Name: "BillingClient"}},
		},
	})
	reg.AddIdentity("order-service", &model.ServiceIdentity{
		ServiceName: "order-service",
		Aliases:     []model.IdentityAlias{{Kind: "dns", Value: "order-service.example.global"}},
	})

	reg.AddArchitecture("billing-service", &model.ServiceArchitecture{
		ServiceName: "billing-service",
		RepoPath:    "/repos/billing-service",
		Exposures:   []model.Exposure{{BaseEntity: model.BaseEntity{ID: "e2"}}},
	})

	resolution := &resolver.Resolution{
		Matches: []resolver.ResolvedMatch{
			{
				FromService:    "order-service",
				DependencyID:   "d1",
				DependencyName: "BillingClient",
				DependencyType: "outbound_http",
				ToService:      "billing-service",
				MatchType:      "http",
				Confidence:     0.85,
				Reasoning:      "deterministic: dns match",
			},
		},
	}

	log := util.NewLogger(util.LevelInfo)
	builder := NewBuilder(reg, log)
	g := builder.Build(resolution)

	if g.Version != "v1alpha1" {
		t.Errorf("expected version v1alpha1, got %s", g.Version)
	}
	if len(g.Services) != 2 {
		t.Errorf("expected 2 services, got %d", len(g.Services))
	}
	if len(g.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(g.Edges))
	}

	edge := g.Edges[0]
	if edge.FromService != "order-service" {
		t.Errorf("expected from_service order-service, got %s", edge.FromService)
	}
	if edge.ToService != "billing-service" {
		t.Errorf("expected to_service billing-service, got %s", edge.ToService)
	}
	if edge.Type != "http" {
		t.Errorf("expected type http, got %s", edge.Type)
	}
}

func TestBuild_UnknownTargetCreatesNode(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("order-service", &model.ServiceArchitecture{
		ServiceName: "order-service",
		RepoPath:    "/repos/order-service",
	})

	resolution := &resolver.Resolution{
		Matches: []resolver.ResolvedMatch{
			{
				FromService: "order-service",
				ToService:   "external-api",
				MatchType:   "http",
				Confidence:  0.7,
			},
		},
	}

	log := util.NewLogger(util.LevelInfo)
	builder := NewBuilder(reg, log)
	g := builder.Build(resolution)

	// external-api should be added as a node.
	if len(g.Services) != 2 {
		t.Errorf("expected 2 services (including external-api), got %d", len(g.Services))
	}

	foundExternal := false
	for _, s := range g.Services {
		if s.Name == "external-api" {
			foundExternal = true
		}
	}
	if !foundExternal {
		t.Error("expected external-api to be added to the graph")
	}
}

func TestBuild_SharedResources(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("svc-a", &model.ServiceArchitecture{ServiceName: "svc-a", RepoPath: "/a"})
	reg.AddIdentity("svc-a", &model.ServiceIdentity{
		ServiceName: "svc-a",
		Resources:   []model.OwnedResource{{Kind: "database", Identifier: "shared-db", Role: "writer"}},
	})

	reg.AddArchitecture("svc-b", &model.ServiceArchitecture{ServiceName: "svc-b", RepoPath: "/b"})
	reg.AddIdentity("svc-b", &model.ServiceIdentity{
		ServiceName: "svc-b",
		Resources:   []model.OwnedResource{{Kind: "database", Identifier: "shared-db", Role: "reader"}},
	})

	log := util.NewLogger(util.LevelInfo)
	builder := NewBuilder(reg, log)
	g := builder.Build(&resolver.Resolution{})

	if len(g.SharedResources) != 1 {
		t.Errorf("expected 1 shared resource, got %d", len(g.SharedResources))
	}
	if len(g.SharedResources) > 0 {
		sr := g.SharedResources[0]
		if sr.Kind != "database" {
			t.Errorf("expected shared resource kind 'database', got %s", sr.Kind)
		}
		if len(sr.Services) != 2 {
			t.Errorf("expected 2 services sharing the resource, got %d", len(sr.Services))
		}
	}
}

func TestBuild_EmptyResolution(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("lonely-service", &model.ServiceArchitecture{
		ServiceName: "lonely-service",
		RepoPath:    "/repos/lonely",
	})

	log := util.NewLogger(util.LevelInfo)
	builder := NewBuilder(reg, log)
	g := builder.Build(&resolver.Resolution{})

	if len(g.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(g.Services))
	}
	if len(g.Edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(g.Edges))
	}
}
