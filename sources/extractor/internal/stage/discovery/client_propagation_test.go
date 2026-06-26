package discovery

import (
	"os"
	"path/filepath"
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

func TestResolveClientRefDBUsesDeploymentProfileConsensus(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application-dev.yml": cfg("application-dev.yml",
			"spring.datasource.url", "jdbc:postgresql://localhost:5432/routing_test",
		),
		"application-stage.yml": cfg("application-stage.yml",
			"spring.datasource.url", "jdbc:postgresql://${ROUTING_DB_HOST}/${ROUTING_DB_NAME}",
		),
		"application-production.yml": cfg("application-production.yml",
			"spring.datasource.url", "jdbc:postgresql://${ROUTING_DB_HOST}/${ROUTING_DB_NAME}",
		),
	}}
	ref := resolveClientRef(idx, model.ConnectionClient{LogicalName: "factsRepository", Kind: "db", ConfigAnchor: "spring.datasource.url"})
	if ref == nil {
		t.Fatal("expected deployment-profile datasource identity")
	}
	if ref.Kind != "postgres" || ref.Database != "routing" || ref.LogicalName != "routing" {
		t.Fatalf("ref = %+v, want postgres routing", ref)
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

func TestPropagateClientInstancesKindFallbackRespectsConcretePlatform(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml", "spring.datasource.url", "jdbc:postgresql://pg:5432/orders_db"),
	}}
	clients := []model.ConnectionClient{{LogicalName: "ds", Kind: "db", ConfigAnchor: "spring.datasource.url"}}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{
			Type:     "db_operation",
			Name:     "write traffic-info",
			Platform: "dynamodb",
			Instance: "dynamodb",
			Details:  map[string]any{"table": "traffic-info", "operation": "write", "platform": "dynamodb"},
		}},
	}
	PropagateClientInstances(idx, clients, nil, deps)
	if deps[0].Platform != "dynamodb" || deps[0].Instance != "dynamodb" {
		t.Fatalf("dynamodb operation was incorrectly stamped by postgres client: %+v", deps[0].BaseEntity)
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

func TestPropagateClientInstancesHTTPClientFromHandlerReplacesOperationLabel(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml", "services.billing-service.url", "${BILLING_URL:https://billing-service.internal}"),
	}}
	clients := []model.ConnectionClient{{LogicalName: "BillingClient", Kind: "http", ConfigAnchor: "services.billing-service.url"}}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{
			Type:     "outbound_http",
			Name:     "POST /charge",
			Platform: "http",
			Instance: "POST /charge",
			Details:  map[string]any{"method": "POST", "path": "/charge", "handler": "BillingClient.create"},
		}},
	}
	PropagateClientInstances(idx, clients, nil, deps)
	got := deps[0]
	if got.Instance != "billing-service" {
		t.Fatalf("instance = %q, want billing-service", got.Instance)
	}
	if got.InstanceRef == nil || got.InstanceRef.LogicalName != "billing-service" {
		t.Fatalf("instance_ref = %+v, want billing-service", got.InstanceRef)
	}
	if got.Details["target_service"] != "billing-service" || got.Details["url_template"] == "" {
		t.Fatalf("details did not receive target/url: %+v", got.Details)
	}
}

func TestPropagateClientInstancesHTTPClientUsesConfigAnchorWhenProfileURLAmbiguous(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml",
			"services.publisher-api.url", "${PUBLISHER_API_URL:https://publisher-api.local}",
			"services.content-store.url", "${CONTENT_STORE_URL:https://content-store.local}",
		),
		"application-stage.yml": cfg("application-stage.yml",
			"services.publisher-api.url", "https://stage-publisher.example",
			"services.content-store.url", "https://stage-content.example",
		),
	}}
	clients := []model.ConnectionClient{
		{LogicalName: "PublisherApiClient", Symbol: "PublisherApiClient", Kind: "http", ConfigAnchor: "services.publisher-api.url"},
		{LogicalName: "ContentStoreApiClient", Symbol: "ContentStoreApiClient", Kind: "http", ConfigAnchor: "services.content-store.url"},
	}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{
			Type:     "outbound_http",
			Name:     "GET /publishers/{id}",
			Platform: "http",
			Instance: "GET /publishers/{id}",
			Details:  map[string]any{"method": "GET", "path": "/publishers/{id}", "handler": "PublisherApiClient.getPublisherById"},
		}},
		{BaseEntity: model.BaseEntity{
			Type:     "outbound_http",
			Name:     "GET /catalogCampaign",
			Platform: "http",
			Instance: "GET /catalogCampaign",
			Details:  map[string]any{"method": "GET", "path": "/catalogCampaign", "handler": "ContentStoreApiClient.getContentByCampaignId"},
		}},
	}

	PropagateClientInstances(idx, clients, nil, deps)

	if got := deps[0].Instance; got != "publisher-api" {
		t.Fatalf("publisher dependency instance = %q, want publisher-api", got)
	}
	if got := deps[1].Instance; got != "content-store" {
		t.Fatalf("content dependency instance = %q, want content-store", got)
	}
	if deps[0].InstanceRef == nil || deps[0].InstanceRef.ConfigSource != "services.publisher-api.url" {
		t.Fatalf("publisher dependency ref = %+v", deps[0].InstanceRef)
	}
	if deps[1].InstanceRef == nil || deps[1].InstanceRef.ConfigSource != "services.content-store.url" {
		t.Fatalf("content dependency ref = %+v", deps[1].InstanceRef)
	}
}

func TestResolveClientRefUsesConfiguredHTTPTargetAlias(t *testing.T) {
	repo := t.TempDir()
	writeServiceConfig(t, repo, `
http_targets:
  - id: catalogue
    service_ref: service.checkout-service
    client_class: CatalogueApiClient
    config_key: services.catalogue.url
`)
	idx := &astpkg.ProjectIndex{
		RepoRoot: repo,
		Configs: map[string]*astpkg.ConfigFile{
			"application.yml": cfg("application.yml", "services.catalogue.url", "${FMA_URL:https://checkout-service.internal}"),
		},
	}

	ref := resolveClientRef(idx, model.ConnectionClient{
		LogicalName:  "catalogueClient",
		Symbol:       "com.example.CatalogueApiClient",
		Kind:         "http",
		ConfigAnchor: "services.catalogue.url",
	})
	if ref == nil {
		t.Fatal("expected configured http target to resolve")
	}
	if ref.LogicalName != "checkout-service" {
		t.Fatalf("logical name = %q, want checkout-service", ref.LogicalName)
	}
	if ref.URLTemplate == "" || ref.ConfigSource != "services.catalogue.url" {
		t.Fatalf("ref did not carry URL/config source: %+v", ref)
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

func writeServiceConfig(t *testing.T, repo, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "diffmind-configuration.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
