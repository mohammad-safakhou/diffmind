package archgraph

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestRelationshipConfidenceUsesWeakestKnownSignal(t *testing.T) {
	got, ok := relationshipConfidence(EntitySummary{Details: map[string]any{"detection_confidence": 1.0, "resolution_confidence": 0.84}})
	if !ok || got != 0.84 {
		t.Fatalf("confidence = %v, %v; want 0.84, true", got, ok)
	}
	if _, ok := relationshipConfidence(EntitySummary{}); ok {
		t.Fatal("unknown confidence must remain unknown")
	}
}

func TestRedisResourceIdentityScopesUnknownAndMergesExplicit(t *testing.T) {
	a, _, _ := graphResourceNodeIdentity("cache", "redis", "alpha-service", dbRef{name: "redis"})
	b, _, _ := graphResourceNodeIdentity("cache", "redis", "beta-service", dbRef{name: "redis"})
	if a == b {
		t.Fatalf("unknown Redis clients collapsed to %q", a)
	}
	sharedA, _, _ := graphResourceNodeIdentity("cache", "redis", "alpha-service", dbRef{name: "shared.example.test:6379"})
	sharedB, _, _ := graphResourceNodeIdentity("cache", "redis", "beta-service", dbRef{name: "shared.example.test:6379"})
	if sharedA != sharedB {
		t.Fatalf("identical explicit Redis identities did not merge: %q != %q", sharedA, sharedB)
	}
}

func TestBuildRouteFallbackCannotOverrideExplicitHTTPDestination(t *testing.T) {
	root := t.TempDir()
	caller := filepath.Join(root, "caller")
	owner := filepath.Join(root, "owner")
	for path, body := range map[string]string{
		caller: `{"schema":"diffmind.service.v1","service":{"id":"caller","name":"caller"},"repository":{"commit":"abc"},"objects":{"http_calls":[{"id":"call","kind":"http_call","name":"GET invoices","method":"GET","url_template":"https://external.example.test/invoices","target":{"type":"service","ref":"external.example.test"},"confidence":"high","evidence_refs":[]}]},"flows":[],"observations":[],"evidence":[]}`,
		owner:  `{"schema":"diffmind.service.v1","service":{"id":"route-owner","name":"route-owner"},"repository":{"commit":"def"},"objects":{"http_endpoints":[{"id":"route","kind":"http_endpoint","name":"GET /invoices","method":"GET","path":"/invoices","confidence":"high","evidence_refs":[]}]},"flows":[],"observations":[],"evidence":[]}`,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "diffmind.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	graph := BuildWithSupplements("run", map[string]string{"caller": caller, "route-owner": owner}, nil)
	var external, incorrect bool
	for _, edge := range graph.Edges {
		if edge.From == "caller" && edge.To == "example.test" && edge.Type == "http" {
			external = true
		}
		if edge.From == "caller" && edge.To == "route-owner" && edge.Type == "http" {
			incorrect = true
		}
	}
	if !external || incorrect {
		for _, edge := range graph.Edges {
			t.Logf("edge: %+v", *edge)
		}
		t.Fatalf("explicit destination route handling external=%v incorrect=%v edges=%+v", external, incorrect, graph.Edges)
	}
}
