package discovery

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// The template mirrors the real-run artifact that motivated this file: one
// physical postgres DB emitted as "spring.datasource.url", "unknown", and a
// rendered Go map — three identities for one instance.
const atsURLTemplate = "jdbc:postgresql://${DATABASE_HOST:localhost}:${DATABASE_PORT:5432}/${DATABASE_NAME:routing_db}"

func atsIndex() *astpkg.ProjectIndex {
	return &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": cfg("application.yml",
			"spring.datasource.url", atsURLTemplate,
			"services.aws.sqs.catalogue-target-request.url", "${CATALOG_SQS:http://localhost:4566/catalogue-target-request-sqs}",
			"services.aws.sqs.catalog-target-response-sqs.url", "${CATALOG_RESPONSE_SQS:http://localhost:4566/catalogue-target-response-sqs}",
			"services.salesforce-account-api.url", "https://salesforce-data-api.example.global",
		),
	}}
}

func TestSingleDatastoreRef(t *testing.T) {
	ref := singleDatastoreRef(atsIndex())
	if ref == nil {
		t.Fatal("want datastore ref, got nil")
	}
	if ref.Kind != "postgres" || ref.Database != "routing_db" || ref.LogicalName != "routing_db" {
		t.Errorf("got kind=%q database=%q logical=%q", ref.Kind, ref.Database, ref.LogicalName)
	}
	if ref.URLTemplate != atsURLTemplate {
		t.Errorf("template must be preserved verbatim, got %q", ref.URLTemplate)
	}
	if ref.ResolvedURL != "" || ref.Host != "" {
		t.Errorf("placeholder template must not claim resolved url/host, got %q/%q", ref.ResolvedURL, ref.Host)
	}
	if ref.ConfigSource != "application.yml: spring.datasource.url" {
		t.Errorf("got config source %q", ref.ConfigSource)
	}

	// Two distinct connection URLs -> ambiguous -> nil (invariant #5/#6).
	multi := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"a.yml": cfg("a.yml", "x.url", "jdbc:postgresql://h/d1"),
		"b.yml": cfg("b.yml", "y.url", "jdbc:postgresql://h/d2"),
	}}
	if got := singleDatastoreRef(multi); got != nil {
		t.Errorf("ambiguous datastores must not stamp, got %+v", got)
	}

	// Same URL repeated across profile files is still ONE datastore.
	dup := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml":      cfg("application.yml", "spring.datasource.url", "jdbc:postgresql://db:5432/ats"),
		"application-prod.yml": cfg("application-prod.yml", "spring.datasource.url", "jdbc:postgresql://db:5432/ats"),
	}}
	ref = singleDatastoreRef(dup)
	if ref == nil || ref.Database != "ats" {
		t.Fatalf("duplicate URL across profiles must stamp, got %+v", ref)
	}
	if ref.ResolvedURL != "jdbc:postgresql://db:5432/ats" || ref.Host != "db:5432" {
		t.Errorf("placeholder-free URL should resolve fully, got url=%q host=%q", ref.ResolvedURL, ref.Host)
	}
}

func TestStampInstanceRefsConvergesDatastoreSpellings(t *testing.T) {
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "db_operation", Platform: "postgres", Instance: "spring.datasource.url"}},
		{BaseEntity: model.BaseEntity{Type: "db_operation", Platform: "postgres", Instance: "unknown"}},
		{BaseEntity: model.BaseEntity{Type: "db_operation", Platform: "postgres", Instance: "map[connection_source:spring.datasource.url url_template:" + atsURLTemplate + "]"}},
		{BaseEntity: model.BaseEntity{Type: "db_operation", Platform: "postgres", Instance: "postgres"}},   // classify's platform fallback
		{BaseEntity: model.BaseEntity{Type: "db_operation", Platform: "postgres", Instance: "billing_db"}}, // concrete -> kept
		{BaseEntity: model.BaseEntity{Type: "queue_publish", Platform: "sqs", Instance: "catalogue-target-request-sqs"}},
	}
	StampInstanceRefs(atsIndex(), nil, deps)

	for i := 0; i < 4; i++ {
		if deps[i].Instance != "routing_db" {
			t.Errorf("dep[%d]: generic spelling must converge, got %q", i, deps[i].Instance)
		}
		if deps[i].InstanceRef == nil || deps[i].InstanceRef.URLTemplate != atsURLTemplate {
			t.Errorf("dep[%d]: missing or wrong instance ref: %+v", i, deps[i].InstanceRef)
		}
	}
	if deps[4].Instance != "billing_db" {
		t.Errorf("concrete instance must be kept, got %q", deps[4].Instance)
	}
	q := deps[5]
	if q.Instance != "catalogue-target-request-sqs" {
		t.Errorf("queue instance must be untouched, got %q", q.Instance)
	}
	if q.InstanceRef == nil || q.InstanceRef.Kind != "sqs" ||
		q.InstanceRef.URLTemplate != "${CATALOG_SQS:http://localhost:4566/catalogue-target-request-sqs}" {
		t.Errorf("queue ref should carry the config URL template, got %+v", q.InstanceRef)
	}
}

func TestStampInstanceRefsQueueConsumerExposure(t *testing.T) {
	exposures := []model.Exposure{
		{BaseEntity: model.BaseEntity{Type: "queue_consumer", Platform: "sqs", Instance: "catalogue-target-request-sqs"}},
		{BaseEntity: model.BaseEntity{Type: "http_route", Platform: "http", Instance: "inbound-http"}},
	}
	StampInstanceRefs(atsIndex(), exposures, nil)
	ref := exposures[0].InstanceRef
	if ref == nil || ref.LogicalName != "catalogue-target-request-sqs" || ref.ConfigSource == "" {
		t.Errorf("consumer should get broker ref, got %+v", ref)
	}
	if exposures[1].InstanceRef != nil {
		t.Errorf("http_route must not get an instance ref")
	}
}

func TestResolveQueueLogicalNameHygiene(t *testing.T) {
	idx := atsIndex()
	cases := []struct {
		name   string
		entity model.BaseEntity
		want   string
	}{
		{
			name: "config property with prose",
			entity: model.BaseEntity{
				Instance: "services.aws.sqs.catalog-target-response-sqs.url (default local URL follows)",
			},
			want: "catalogue-target-response-sqs",
		},
		{
			name: "config detail overrides clean LLM label",
			entity: model.BaseEntity{
				Instance: "target-calculation-events-sqs-consumer",
				Details: map[string]any{
					"queue": "services.aws.sqs.catalog-target-response-sqs.url (default local URL follows)",
				},
			},
			want: "catalogue-target-response-sqs",
		},
		{
			name:   "clean ungrounded instance fallback",
			entity: model.BaseEntity{Instance: "orders-created"},
			want:   "orders-created",
		},
		{
			name:   "sentinel",
			entity: model.BaseEntity{Instance: "_none_"},
			want:   "",
		},
		{
			name:   "ungrounded prose",
			entity: model.BaseEntity{Instance: "some queue described in prose"},
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveQueueLogicalName(idx, &tc.entity); got != tc.want {
				t.Fatalf("resolveQueueLogicalName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStampQueueInstanceConvergesIdentity(t *testing.T) {
	exposures := []model.Exposure{
		{BaseEntity: model.BaseEntity{
			ID: "deterministic", Type: "queue_consumer", Name: "catalogue-target-response-sqs",
			Platform: "sqs", Instance: "catalogue-target-response-sqs", Operation: "consume catalogue-target-response-sqs",
			Details: map[string]any{"queue": "catalogue-target-response-sqs"},
		}},
		{BaseEntity: model.BaseEntity{
			ID: "base", Type: "queue_consumer", Name: "target-calculation-events-sqs-consumer",
			Platform: "sqs", Instance: "target-calculation-events-sqs-consumer",
			Operation: "consume target-calculation-events-sqs-consumer",
			Details: map[string]any{
				"queue": "services.aws.sqs.catalog-target-response-sqs.url (default local URL follows)",
			},
		}},
	}

	StampInstanceRefs(atsIndex(), exposures, nil)
	if got := exposures[1].Instance; got != "catalogue-target-response-sqs" {
		t.Fatalf("LLM variant instance = %q, want physical queue name", got)
	}
	if got := exposures[1].Details["queue"]; got != "catalogue-target-response-sqs" {
		t.Fatalf("LLM variant queue detail = %v, want physical queue name", got)
	}
	if got := exposures[1].Details["queue_raw"]; got == nil {
		t.Fatal("original queue detail must be preserved")
	}
}

func TestCleanResourceToken(t *testing.T) {
	cases := map[string]bool{
		"orders-created":   true,
		"_none_":           false,
		"stream-none":      false,
		"not-found":        false,
		"queue with prose": false,
		"a -> b":           false,
		"https://x/q":      false,
		"${queue.url}":     false,
	}
	for in, want := range cases {
		if got := cleanResourceToken(in); got != want {
			t.Errorf("cleanResourceToken(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStampOutboundHTTPInstance(t *testing.T) {
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "outbound_http", Platform: "http", Instance: "salesforce-account-api"}},
	}
	StampInstanceRefs(atsIndex(), nil, deps)
	ref := deps[0].InstanceRef
	if ref == nil || ref.ResolvedURL != "https://salesforce-data-api.example.global" || ref.Host != "salesforce-data-api.example.global" {
		t.Fatalf("outbound ref should resolve the configured base URL, got %+v", ref)
	}

	// Per-environment variants -> logical name only (profiles are V3a's job).
	multi := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml":       cfg("application.yml", "services.billing-api.url", "https://billing.example.com"),
		"application-stage.yml": cfg("application-stage.yml", "services.billing-api.url", "https://billing.stage.example.com"),
	}}
	deps = []model.Dependency{
		{BaseEntity: model.BaseEntity{Type: "outbound_http", Platform: "http", Instance: "billing-api"}},
	}
	StampInstanceRefs(multi, nil, deps)
	ref = deps[0].InstanceRef
	if ref == nil || ref.LogicalName != "billing-api" {
		t.Fatalf("ambiguous URLs should still carry logical name, got %+v", ref)
	}
	if ref.URLTemplate != "" || ref.ResolvedURL != "" {
		t.Errorf("ambiguous URLs must not pick a winner, got %+v", ref)
	}
}

func TestGenericInstance(t *testing.T) {
	idx := atsIndex()
	cases := []struct {
		instance, platform string
		want               bool
	}{
		{"", "postgres", true},
		{"unknown", "postgres", true},
		{"postgres", "postgres", true},              // bare platform word
		{"spring.datasource.url", "postgres", true}, // config key present in index
		{"app.db.primary.url", "postgres", true},    // dotted key-ish even when not indexed
		{"map[url_template:jdbc:x]", "postgres", true},
		{"routing_db", "postgres", false}, // concrete database name
		{"public.orders schema", "postgres", false}, // contains a space -> not a key
	}
	for _, c := range cases {
		if got := genericInstance(idx, c.instance, c.platform); got != c.want {
			t.Errorf("genericInstance(%q): want %v, got %v", c.instance, c.want, got)
		}
	}
}

func TestDatabaseFromConnectionURL(t *testing.T) {
	idx := atsIndex()
	cases := []struct{ url, want string }{
		{atsURLTemplate, "routing_db"},      // ${NAME:default} -> default
		{"jdbc:postgresql://db:5432/ats", "ats"},      // plain
		{"jdbc:mysql://db/shop?useSSL=false", "shop"}, // query params stripped
		{"mongodb://h:27017/catalog", "catalog"},      // non-jdbc scheme
		{"jdbc:postgresql://${HOST}/${ROUTING_DB_NAME}", "routing"},
		{"jdbc:postgresql://db:5432/", ""}, // no db segment
		{"redis://cache:6379", ""},         // no path
	}
	for _, c := range cases {
		if got := databaseFromConnectionURL(idx, c.url); got != c.want {
			t.Errorf("databaseFromConnectionURL(%q): want %q, got %q", c.url, c.want, got)
		}
	}
}
