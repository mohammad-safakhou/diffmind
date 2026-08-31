package resolver

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/model"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/registry"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/util"
)

func setupTestRegistry() *registry.Registry {
	reg := registry.New()

	// Order service: depends on billing-service.example.global.
	orderArch := &model.ServiceArchitecture{
		ServiceName: "order-service",
		RepoPath:    "/repos/order-service",
		Exposures: []model.Exposure{
			{BaseEntity: model.BaseEntity{ID: "exp1", Type: "http_route", Name: "POST /api/orders"}},
		},
		Dependencies: []model.Dependency{
			{BaseEntity: model.BaseEntity{
				ID:   "dep1",
				Type: "outbound_http",
				Name: "BillingClient.charge",
				Tags: []string{"billing-service"},
				Evidence: []model.Evidence{
					{Snippet: "url: https://billing-service.example.global/api/charge"},
				},
			}},
			{BaseEntity: model.BaseEntity{
				ID:      "dep2",
				Type:    "db_operation",
				Name:    "OrderRepository.save",
				Tags:    []string{"postgresql", "table:orders"},
				Details: map[string]any{"database": "orders-db"},
			}},
		},
	}
	reg.AddArchitecture("order-service", orderArch)
	reg.AddIdentity("order-service", &model.ServiceIdentity{
		ServiceName: "order-service",
		Aliases: []model.IdentityAlias{
			{Kind: "dns", Value: "order-service.example.global"},
		},
	})

	// Billing service: exposes POST /api/charge.
	billingArch := &model.ServiceArchitecture{
		ServiceName: "billing-service",
		RepoPath:    "/repos/billing-service",
		Exposures: []model.Exposure{
			{BaseEntity: model.BaseEntity{ID: "exp2", Type: "http_route", Name: "POST /api/charge"}},
		},
		Dependencies: []model.Dependency{
			{BaseEntity: model.BaseEntity{
				ID:   "dep3",
				Type: "outbound_http",
				Name: "NotificationClient.send",
				Tags: []string{"notification-service"},
				Evidence: []model.Evidence{
					{Snippet: "url: https://notification-service.example.global"},
				},
			}},
		},
	}
	reg.AddArchitecture("billing-service", billingArch)
	reg.AddIdentity("billing-service", &model.ServiceIdentity{
		ServiceName: "billing-service",
		Aliases: []model.IdentityAlias{
			{Kind: "dns", Value: "billing-service.example.global"},
			{Kind: "dns", Value: "billing.internal"},
		},
	})

	return reg
}

func TestDeterministicResolution(t *testing.T) {
	reg := setupTestRegistry()
	log := util.NewLogger(util.LevelInfo)
	res := New(reg, log)

	resolution, err := res.Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	// Order service's dependency on billing-service should be resolved.
	if len(resolution.Matches) == 0 {
		t.Fatal("expected at least one match")
	}

	foundBillingMatch := false
	for _, m := range resolution.Matches {
		if m.FromService == "order-service" && m.ToService == "billing-service" {
			foundBillingMatch = true
			if m.MatchType != "http" {
				t.Errorf("expected match type 'http', got %s", m.MatchType)
			}
			if m.Confidence < 0.5 {
				t.Errorf("expected confidence >= 0.5, got %f", m.Confidence)
			}
		}
	}
	if !foundBillingMatch {
		t.Error("expected order-service → billing-service match")
	}
}

func TestUnresolvedDependencies(t *testing.T) {
	reg := setupTestRegistry()
	log := util.NewLogger(util.LevelInfo)
	res := New(reg, log)

	resolution, err := res.Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	// notification-service is not in the registry, so billing's dep should be unresolved.
	foundUnresolved := false
	for _, u := range resolution.Unresolved {
		if u.Service == "billing-service" && u.DependencyName == "NotificationClient.send" {
			foundUnresolved = true
		}
	}
	if !foundUnresolved {
		t.Error("expected billing-service's notification dependency to be unresolved")
	}
}

func TestResolutionFallsBackToRegisteredServiceNames(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("ranking-service", &model.ServiceArchitecture{
		ServiceName: "ranking-service",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep1",
			Type:    "outbound_http",
			Name:    "POST gateway-service /v1/traffic-shaper",
			Details: map[string]any{"target_service": "gateway-service"},
		}}},
	})
	reg.AddArchitecture("gateway-service", &model.ServiceArchitecture{ServiceName: "gateway-service"})

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 1 {
		t.Fatalf("expected 1 service-name fallback match, got %+v", resolution.Matches)
	}
	match := resolution.Matches[0]
	if match.FromService != "ranking-service" || match.ToService != "gateway-service" || match.MatchType != "http" {
		t.Fatalf("unexpected match: %+v", match)
	}
}

func TestResolutionMatchesQueuePublishToConsumerTopic(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("publisher", &model.ServiceArchitecture{
		ServiceName: "publisher",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep-queue",
			Type:    "queue_publish",
			Name:    "publish order",
			Details: map[string]any{"topic": "arn:aws:sns:eu-central-1:123:order-events.fifo"},
		}}},
	})
	reg.AddArchitecture("consumer", &model.ServiceArchitecture{
		ServiceName: "consumer",
		Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
			ID:      "exp-queue",
			Type:    "queue_consumer",
			Name:    "consume order",
			Details: map[string]any{"queue": "https://sqs.eu-central-1.amazonaws.com/123/order_events.fifo"},
		}}},
	})

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 1 {
		t.Fatalf("expected 1 queue match, got %+v", resolution.Matches)
	}
	match := resolution.Matches[0]
	if match.ToService != "consumer" || match.MatchType != "queue" || match.Confidence != 0.9 {
		t.Fatalf("unexpected queue match: %+v", match)
	}
}

func TestResolutionAvoidsShortSubstringMatch(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("caller", &model.ServiceArchitecture{
		ServiceName: "caller",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep-http",
			Type:    "outbound_http",
			Name:    "GET /api/widgets",
			Details: map[string]any{"target_service": "api"},
		}}},
	})
	reg.AddArchitecture("inventory-api", &model.ServiceArchitecture{ServiceName: "inventory-api"})

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 0 {
		t.Fatalf("short substring should not match a service name: %+v", resolution.Matches)
	}
	if len(resolution.Unresolved) != 1 {
		t.Fatalf("expected unresolved dependency, got %+v", resolution.Unresolved)
	}
}

func TestResolutionNormalizesHostnameTargets(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("caller", &model.ServiceArchitecture{
		ServiceName: "caller",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep-http",
			Type:    "outbound_http",
			Name:    "call billing",
			Details: map[string]any{"url": "lb://billing-service.internal:8080/v1/charge"},
		}}},
	})
	reg.AddArchitecture("billing-service", &model.ServiceArchitecture{ServiceName: "billing-service"})

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 1 || resolution.Matches[0].ToService != "billing-service" {
		t.Fatalf("expected normalized hostname match, got %+v", resolution.Matches)
	}
}

func TestResolutionMatchesHTTPRouteExposure(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("routing-service", &model.ServiceArchitecture{
		ServiceName: "routing-service",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep-http",
			Type:    "outbound_http",
			Name:    "PUT /traffic-info/{catalogueCampaignId}/placements",
			Details: map[string]any{"method": "PUT", "url_template": "/traffic-info/{catalogueCampaignId}/placements"},
		}}},
	})
	reg.AddArchitecture("pricing-service", &model.ServiceArchitecture{
		ServiceName: "pricing-service",
		Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
			ID:   "exp-http",
			Type: "http_route",
			Name: "PUT /traffic-info/{campaignId}/placements",
		}}},
	})

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 1 {
		t.Fatalf("expected one route match, got %+v", resolution.Matches)
	}
	match := resolution.Matches[0]
	if match.ToService != "pricing-service" || match.MatchType != "http" || match.Confidence != 0.92 {
		t.Fatalf("unexpected route match: %+v", match)
	}
}

func TestResolutionMatchesHTTPRouteAfterPrefixNormalization(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("caller", &model.ServiceArchitecture{
		ServiceName: "caller",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep-http",
			Type:    "outbound_http",
			Name:    "GET /api/v1/public/widgets/{widgetId}",
			Details: map[string]any{"method": "GET", "path": "/api/v1/public/widgets/{widgetId}"},
		}}},
	})
	reg.AddArchitecture("inventory-api", &model.ServiceArchitecture{
		ServiceName: "inventory-api",
		Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
			ID:   "exp-http",
			Type: "http_route",
			Name: "GET /widgets/:id",
		}}},
	})

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 1 {
		t.Fatalf("expected one normalized route match, got %+v", resolution.Matches)
	}
	match := resolution.Matches[0]
	if match.ToService != "inventory-api" || match.MatchType != "http" || match.Confidence != 0.84 {
		t.Fatalf("unexpected normalized route match: %+v", match)
	}
}

func TestResolutionDoesNotGuessAmbiguousHTTPRoute(t *testing.T) {
	reg := registry.New()
	reg.AddArchitecture("caller", &model.ServiceArchitecture{
		ServiceName: "caller",
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID:      "dep-http",
			Type:    "outbound_http",
			Name:    "GET /health",
			Details: map[string]any{"method": "GET", "path": "/health"},
		}}},
	})
	for _, service := range []string{"service-a", "service-b"} {
		reg.AddArchitecture(service, &model.ServiceArchitecture{
			ServiceName: service,
			Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
				ID:   "exp-" + service,
				Type: "http_route",
				Name: "GET /health",
			}}},
		})
	}

	resolution, err := New(reg, util.NewLogger(util.LevelInfo)).Resolve()
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resolution.Matches) != 0 {
		t.Fatalf("ambiguous route should not match: %+v", resolution.Matches)
	}
	if len(resolution.Unresolved) != 1 {
		t.Fatalf("expected unresolved ambiguous dependency, got %+v", resolution.Unresolved)
	}
}

func TestExtractTarget(t *testing.T) {
	tests := []struct {
		name     string
		dep      model.Dependency
		expected string
	}{
		{
			name: "from details url",
			dep: model.Dependency{BaseEntity: model.BaseEntity{
				Details: map[string]any{"url": "https://billing.internal/charge"},
			}},
			expected: "https://billing.internal/charge",
		},
		{
			name: "from tags",
			dep: model.Dependency{BaseEntity: model.BaseEntity{
				Tags: []string{"feign", "billing-service"},
			}},
			expected: "billing-service",
		},
		{
			name: "from evidence",
			dep: model.Dependency{BaseEntity: model.BaseEntity{
				Name: "SomeClient",
				Evidence: []model.Evidence{
					{Snippet: "url: https://some-api.example.global/v2"},
				},
			}},
			expected: "https://some-api.example.global/v2",
		},
		{
			name: "from top-level instance",
			dep: model.Dependency{BaseEntity: model.BaseEntity{
				Instance: "routing_db",
				Details:  map[string]any{"database_name": "ignored"},
			}},
			expected: "routing_db",
		},
		{
			name: "from new details target service",
			dep: model.Dependency{BaseEntity: model.BaseEntity{
				Details: map[string]any{"target_service": "routing-api"},
			}},
			expected: "routing-api",
		},
		{
			name: "from new details table entity",
			dep: model.Dependency{BaseEntity: model.BaseEntity{
				Details: map[string]any{"table_or_entity": "traffic_configuration_history"},
			}},
			expected: "traffic_configuration_history",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := extractTarget(&tt.dep)
			if target != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, target)
			}
		})
	}
}
