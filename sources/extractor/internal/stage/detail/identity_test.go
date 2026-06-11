package detail

import "testing"

// C2: detail must not be able to change an entity's identity-bearing details.
// A seed with method+path, enriched by a response that "normalized" the path to
// drop the class base path, must keep the seed's path (identity invariant), while
// non-identity enrichment still flows through.
func TestPinIdentityDetailsKeepsSeedRoute(t *testing.T) {
	seed := llmEntity{
		Type: "http_route",
		Name: "GET /api/users/{id}",
		Details: map[string]any{
			"method": "GET",
			"path":   "/api/users/{id}",
		},
	}
	item := &llmEntity{
		Type: "http_route",
		Details: map[string]any{
			"path":          "/users/{id}", // detail dropped the base path
			"response_type": "UserDTO",     // legitimate enrichment
		},
	}
	PinIdentityDetails(item, seed)

	if item.Details["path"] != "/api/users/{id}" {
		t.Errorf("path not pinned to seed: %v", item.Details["path"])
	}
	if item.Details["method"] != "GET" {
		t.Errorf("method not filled from seed: %v", item.Details["method"])
	}
	if item.Details["response_type"] != "UserDTO" {
		t.Errorf("non-identity enrichment was lost: %v", item.Details["response_type"])
	}
}

// When the seed lacks an identity field, detail may establish it (fill, not
// change).
func TestPinIdentityDetailsFillsWhenSeedEmpty(t *testing.T) {
	seed := llmEntity{Type: "db_operation", Name: "read orders", Details: map[string]any{"operation": "read"}}
	item := &llmEntity{Type: "db_operation", Details: map[string]any{"operation": "read", "table": "orders"}}
	PinIdentityDetails(item, seed)
	if item.Details["table"] != "orders" {
		t.Errorf("detail should establish table the seed lacked: %v", item.Details["table"])
	}
	if item.Details["operation"] != "read" {
		t.Errorf("operation should remain read: %v", item.Details["operation"])
	}
}

func TestIdentityDetailKeys(t *testing.T) {
	if got := IdentityDetailKeys("HTTP_ROUTE"); len(got) != 2 || got[0] != "method" || got[1] != "path" {
		t.Errorf("http_route identity keys wrong: %v", got)
	}
	if IdentityDetailKeys("unknown_type") != nil {
		t.Error("unknown type should have no identity detail keys")
	}
}
