package extraction

import (
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/objectives"
)

// Structured detail values must never be rendered into grouping fields: a real
// run emitted instance "map[connection_source:... url_template:jdbc:...]" when
// imported connection details arrived as an object, splitting one database into
// several downstream identities. The table is the RESOURCE (identity key), not
// the instance, so it must NOT become the instance either — the instance is the
// physical datastore, left generic here and resolved deterministically later.
func TestDeriveGroupingIgnoresStructuredDetails(t *testing.T) {
	b := model.BaseEntity{
		Type: "db_operation",
		Name: "write traffic_configuration",
		Details: map[string]any{
			"connection_string": map[string]any{
				"url_template": "jdbc:postgresql://${DATABASE_HOST}/${DATABASE_NAME}",
				"type":         "PostgreSQL",
			},
			"table":     "traffic_configuration",
			"operation": "insert",
		},
	}
	platform, instance, _, opKind := DeriveGrouping(b)
	if strings.HasPrefix(instance, "map[") {
		t.Fatalf("structured detail leaked into instance: %q", instance)
	}
	if instance == "traffic_configuration" {
		t.Errorf("the table is the resource, not the instance; it must not be used as the instance, got %q", instance)
	}
	if instance != "database" {
		t.Errorf("instance should fall back to the generic platform (resolved deterministically later), got %q", instance)
	}
	if platform != "database" {
		// Generic here is correct: StampInferredDBPlatform later fills the
		// configured engine; classify must not guess from a structured value.
		t.Errorf("platform should stay generic without a scalar hint, got %q", platform)
	}
	if opKind != "write" {
		t.Errorf("operation_kind should be canonical write, got %q", opKind)
	}
}

func TestDeriveGroupingPreservesS3ObjectStorageCacheOperation(t *testing.T) {
	b := model.BaseEntity{
		Type: "cache_operation",
		Name: "write dynamic-uploads",
		Tags: []string{"deterministic", "object-storage:s3", "aws-sdk"},
		Details: map[string]any{
			"cache":      "dynamic-uploads",
			"cache_type": "object_storage",
			"operation":  "write",
			"platform":   "s3",
		},
	}
	platform, instance, _, opKind := DeriveGrouping(b)
	if platform != "s3" {
		t.Fatalf("S3 cache operation platform should remain s3, got %q", platform)
	}
	if instance != "dynamic-uploads" {
		t.Fatalf("S3 cache operation instance should be bucket/cache name, got %q", instance)
	}
	if opKind != "write" {
		t.Fatalf("S3 cache operation kind should stay write, got %q", opKind)
	}
}

// P3b: an SQS consumer is a queue_consumer exposure; the old alias routed it
// to the stream_consume dependency, mislabeling the architectural fact.
func TestSQSConsumerAlias(t *testing.T) {
	var queueConsumer, streamConsume objectives.Objective
	for _, o := range objectives.Default() {
		switch o.Type {
		case "queue_consumer":
			queueConsumer = o
		case "stream_consume":
			streamConsume = o
		}
	}
	if _, ok := CanonicalObjectiveType(queueConsumer, "sqs_consumer"); !ok {
		t.Errorf("sqs_consumer must canonicalize to queue_consumer")
	}
	if _, ok := CanonicalObjectiveType(streamConsume, "sqs_consumer"); ok {
		t.Errorf("sqs_consumer must no longer alias to stream_consume")
	}
}

// P1: the structured-output schema pins operation_kind to the closed enum for
// db/cache objectives, so spelling variants are rejected server-side instead
// of splitting identities downstream.
func TestEntitySchemaOperationKindEnum(t *testing.T) {
	var dbObj objectives.Objective
	for _, o := range objectives.Default() {
		if o.Type == "db_operation" {
			dbObj = o
		}
	}
	s := EntitySchemaForObjective(dbObj)
	details := s["properties"].(map[string]any)["details"].(map[string]any)
	enum := details["properties"].(map[string]any)["operation_kind"].(map[string]any)["enum"].([]string)
	if len(enum) != 2 || enum[0] != "read" || enum[1] != "write" {
		t.Errorf("db_operation operation_kind enum wrong: %v", enum)
	}
	if extra, ok := details["additionalProperties"].(bool); ok && !extra {
		t.Errorf("details must stay open for other keys")
	}
}

func TestScalarDetail(t *testing.T) {
	cases := []struct {
		in     any
		want   string
		wantOK bool
	}{
		{"  orders  ", "orders", true},
		{42, "42", true},
		{3.5, "3.5", true},
		{true, "true", true},
		{map[string]any{"a": 1}, "", false},
		{[]any{"a"}, "", false},
		{nil, "", false},
	}
	for _, c := range cases {
		got, ok := scalarDetail(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("scalarDetail(%v): want (%q,%v), got (%q,%v)", c.in, c.want, c.wantOK, got, ok)
		}
	}
}
