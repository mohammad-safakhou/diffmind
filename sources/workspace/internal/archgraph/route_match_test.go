package archgraph

import "testing"

func routeMatchServices() []*ServiceNode {
	return []*ServiceNode{
		{
			Name:  "pricing-service",
			Known: true,
			HTTPRoutes: []EntitySummary{
				{ID: "http.put_traffic_info", Name: "PUT /traffic-info/{catalogueCampaignId}/placements"},
				{ID: "http.get_health", Name: "GET /health"},
			},
		},
		{
			Name:  "placements-management-api",
			Known: true,
			HTTPRoutes: []EntitySummary{
				{ID: "http.get_placements", Name: "GET /placements/{placementId}"},
				{ID: "http.get_health", Name: "GET /health"},
			},
		},
		{
			Name:  "unknown-side",
			Known: false,
			HTTPRoutes: []EntitySummary{
				{ID: "http.hidden", Name: "GET /hidden"},
			},
		},
	}
}

func TestMatchRouteOwnerUniqueRoute(t *testing.T) {
	idx := buildRouteIndex(routeMatchServices())

	dep := EntitySummary{
		ID:   "httpcall.put_traffic_info",
		Name: "PUT /traffic-info/{id}/placements",
	}
	owner, ok := matchRouteOwner(idx, "routing-service", dep)
	if !ok {
		t.Fatalf("expected unique route match, got none")
	}
	if owner.service != "pricing-service" {
		t.Fatalf("expected pricing-service, got %q", owner.service)
	}
	if owner.exposureID != "http.put_traffic_info" {
		t.Fatalf("expected exposure http.put_traffic_info, got %q", owner.exposureID)
	}
}

func TestMatchRouteOwnerAmbiguousRouteRejected(t *testing.T) {
	idx := buildRouteIndex(routeMatchServices())

	dep := EntitySummary{Name: "GET /health"}
	if _, ok := matchRouteOwner(idx, "some-service", dep); ok {
		t.Fatalf("expected multi-owner route to stay unmatched")
	}
}

func TestMatchRouteOwnerSkipsSelfAndUnknownServices(t *testing.T) {
	idx := buildRouteIndex(routeMatchServices())

	// Self-calls must not create a match.
	dep := EntitySummary{Name: "GET /placements/{placementId}"}
	if _, ok := matchRouteOwner(idx, "placements-management-api", dep); ok {
		t.Fatalf("expected self route to stay unmatched")
	}

	// Routes exposed only by non-known services are not indexed.
	dep = EntitySummary{Name: "GET /hidden"}
	if _, ok := matchRouteOwner(idx, "some-service", dep); ok {
		t.Fatalf("expected unknown-service route to stay unmatched")
	}
}

func TestMatchRouteOwnerUsesURLTemplateDetails(t *testing.T) {
	idx := buildRouteIndex(routeMatchServices())

	dep := EntitySummary{
		Name: "call placements",
		Details: map[string]any{
			"method":       "GET",
			"url_template": "${PLACEMENTS_API_URL:https://placements-management-api.example.com}/placements/{placementId}",
		},
	}
	owner, ok := matchRouteOwner(idx, "routing-service", dep)
	if !ok {
		t.Fatalf("expected match from url_template details")
	}
	if owner.service != "placements-management-api" {
		t.Fatalf("expected placements-management-api, got %q", owner.service)
	}
}

func TestMatchRouteOwnerMethodMismatchRejected(t *testing.T) {
	idx := buildRouteIndex(routeMatchServices())

	dep := EntitySummary{Name: "DELETE /traffic-info/{id}/placements"}
	if _, ok := matchRouteOwner(idx, "some-service", dep); ok {
		t.Fatalf("expected method mismatch to stay unmatched")
	}
}

func TestMatchRouteOwnerRootAndEmptyPathsRejected(t *testing.T) {
	idx := buildRouteIndex(routeMatchServices())

	for _, name := range []string{"GET /", "no route here", ""} {
		dep := EntitySummary{Name: name}
		if _, ok := matchRouteOwner(idx, "some-service", dep); ok {
			t.Fatalf("expected %q to stay unmatched", name)
		}
	}
}
