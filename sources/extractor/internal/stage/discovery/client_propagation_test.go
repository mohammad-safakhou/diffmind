package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func multiDatastoreIndex() *astpkg.ProjectIndex {
	return &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml",
			"spring.datasource.url", "jdbc:postgresql://pg:5432/orders_db",
			"spring.mongodb.uri", "mongodb://mongo:27017/audit_db",
		),
	}}
}

// TestResolveClientRefMultiDatastore is the headline capability: per-client
// anchors resolve two distinct datastores independently — something the repo-wide
// singleDatastoreRef explicitly cannot do.
func TestResolveClientRefMultiDatastore(t *testing.T) {
	idx := multiDatastoreIndex()

	pg := resolveClientRef(idx, model.ConnectionClient{LogicalName: "orderRepository", Kind: "db", ConfigAnchor: "spring.datasource.url"})
	if pg == nil || pg.Kind != "postgres" || pg.Database != "orders_db" {
		t.Fatalf("postgres client ref = %+v", pg)
	}
	mongo := resolveClientRef(idx, model.ConnectionClient{LogicalName: "auditRepository", Kind: "db", ConfigAnchor: "spring.mongodb.uri"})
	if mongo == nil || mongo.Kind != "mongodb" || mongo.Database != "audit_db" {
		t.Fatalf("mongo client ref = %+v", mongo)
	}
	if singleDatastoreRef(idx) != nil {
		t.Fatal("two datastores must be ambiguous for the repo-wide resolver (the gap this closes)")
	}
	if r := resolveClientRef(idx, model.ConnectionClient{Kind: "db"}); r != nil {
		t.Fatalf("missing anchor must resolve nil, got %+v", r)
	}
}

// TestPropagateClientInstancesStampsPerClient verifies two same-shaped db ops
// get DISTINCT platform+instance from their respective clients, so they remain
// distinct downstream identities.
func TestPropagateClientInstancesStampsPerClient(t *testing.T) {
	idx := multiDatastoreIndex()
	clients := []model.ConnectionClient{
		{LogicalName: "orderRepository", Symbol: "OrderRepository", Kind: "db", ConfigAnchor: "spring.datasource.url"},
		{LogicalName: "auditRepository", Symbol: "AuditRepository", Kind: "db", ConfigAnchor: "spring.mongodb.uri"},
	}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "read orders", Details: map[string]any{"table": "orders", "operation": "read", "client": "orderRepository"}}},
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "write events", Details: map[string]any{"table": "events", "operation": "write", "client": "auditRepository"}}},
	}

	resolved := PropagateClientInstances(idx, clients, nil, deps)
	if resolved[0].InstanceRef == nil || resolved[1].InstanceRef == nil {
		t.Fatal("clients should carry resolved InstanceRefs")
	}
	if deps[0].Platform != "postgres" || deps[0].Instance != "orders_db" {
		t.Errorf("orders op = %q/%q, want postgres/orders_db", deps[0].Platform, deps[0].Instance)
	}
	if deps[1].Platform != "mongodb" || deps[1].Instance != "audit_db" {
		t.Errorf("events op = %q/%q, want mongodb/audit_db", deps[1].Platform, deps[1].Instance)
	}
}

// TestPropagateClientInstancesKindFallback stamps via a single client of the
// matching kind when an op carries no explicit details.client.
func TestPropagateClientInstancesKindFallback(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml", "spring.datasource.url", "jdbc:postgresql://pg:5432/orders_db"),
	}}
	clients := []model.ConnectionClient{{LogicalName: "ds", Kind: "db", ConfigAnchor: "spring.datasource.url"}}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "read orders", Details: map[string]any{"table": "orders", "operation": "read"}}},
	}
	PropagateClientInstances(idx, clients, nil, deps)
	if deps[0].Platform != "postgres" || deps[0].Instance != "orders_db" {
		t.Errorf("op = %q/%q, want postgres/orders_db via kind fallback", deps[0].Platform, deps[0].Instance)
	}
}

// TestPropagateClientInstancesPlatformDisambiguation is the Phase-1 precision
// fix: two clients of the SAME kind "db" (a Postgres DataSource and a DynamoDB
// client) — which single-of-kind cannot disambiguate (it matches neither) — are
// resolved per-op by the op's platform, so neither op mis-attaches.
func TestPropagateClientInstancesPlatformDisambiguation(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml",
			"spring.datasource.url", "jdbc:postgresql://pg:5432/facts_db",
			"services.routing.dynamodb-table", "traffic-info",
		),
	}}
	clients := []model.ConnectionClient{
		{LogicalName: "automatedFactsRepository", Symbol: "AutomatedFactsCatalogueRepository", Kind: "db", ConfigAnchor: "spring.datasource.url"},
		{LogicalName: "trafficDataClient", Symbol: "software.amazon.awssdk.services.dynamodb.DynamoDbClient", Kind: "db", ConfigAnchor: "services.routing.dynamodb-table"},
	}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "write automated_facts_catalogue", Platform: "postgres", Details: map[string]any{"table": "automated_facts_catalogue", "operation": "write"}}},
		{BaseEntity: model.BaseEntity{Type: "db_operation", Name: "write traffic-info", Platform: "dynamodb", Details: map[string]any{"table": "traffic-info", "operation": "write"}}},
	}
	PropagateClientInstances(idx, clients, nil, deps)
	if deps[0].Instance != "facts_db" {
		t.Errorf("postgres op instance = %q, want facts_db (single-of-kind would match neither)", deps[0].Instance)
	}
	if deps[1].Instance == "facts_db" {
		t.Errorf("dynamodb op mis-attached to the postgres datasource (instance=%q)", deps[1].Instance)
	}
	if deps[1].Platform != "dynamodb" {
		t.Errorf("dynamodb op platform = %q, want dynamodb", deps[1].Platform)
	}
}

// TestPropagateClientInstancesHTTPClient proves stamping now generalizes beyond
// db/cache: an outbound_http op picks up its HTTP client's resolved host/instance.
func TestPropagateClientInstancesHTTPClient(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml", "services.billing.url", "https://billing-service.internal/api"),
	}}
	clients := []model.ConnectionClient{{LogicalName: "billingClient", Kind: "http", ConfigAnchor: "services.billing.url"}}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "outbound_http", Name: "POST /charge", Platform: "http", Instance: "unknown", Details: map[string]any{"method": "POST", "path": "/charge"}}},
	}
	PropagateClientInstances(idx, clients, nil, deps)
	if deps[0].InstanceRef == nil || deps[0].InstanceRef.Host != "billing-service.internal" {
		t.Fatalf("outbound op should inherit the http client's host, got %+v", deps[0].InstanceRef)
	}
}

// TestPropagateClientInstancesEmptyEqualsStampInstanceRefs pins behavior parity:
// with no clients, propagation reduces exactly to StampInstanceRefs.
func TestPropagateClientInstancesEmptyEqualsStampInstanceRefs(t *testing.T) {
	idx := atsIndex()
	mk := func() []model.Dependency {
		return []model.Dependency{
			{BaseEntity: model.BaseEntity{Type: "db_operation", Platform: "postgres", Instance: "unknown", Details: map[string]any{"table": "orders"}}},
		}
	}
	a := mk()
	StampInstanceRefs(idx, nil, a)
	b := mk()
	PropagateClientInstances(idx, nil, nil, b)
	if a[0].Instance != b[0].Instance || a[0].Platform != b[0].Platform {
		t.Fatalf("empty-clients propagation diverged: stamp=%q/%q propagate=%q/%q",
			a[0].Platform, a[0].Instance, b[0].Platform, b[0].Instance)
	}
	if b[0].Instance != "routing_db" {
		t.Fatalf("instance = %q, want routing_db", b[0].Instance)
	}
}
