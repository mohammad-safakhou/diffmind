package pipeline

import (
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	discoverystage "github.com/mohammad-safakhou/diffmind/internal/stage/discovery"
	"github.com/mohammad-safakhou/diffmind/internal/stage/reconcile"
)

func TestQueueInstanceStampingEnablesFinalVariantCollapse(t *testing.T) {
	idx := &astpkg.ProjectIndex{Configs: map[string]*astpkg.ConfigFile{
		"application.yml": {
			Path: "application.yml",
			Entries: []astpkg.ConfigEntry{{
				Key:   "services.aws.sqs.catalog-target-response-sqs.url",
				Value: "${CATALOG_RESPONSE_SQS:http://localhost:4566/catalogue-target-response-sqs}",
			}},
		},
	}}
	exposures := []model.Exposure{
		{BaseEntity: model.BaseEntity{
			ID: "deterministic", Type: "queue_consumer", Name: "catalogue-target-response-sqs",
			Platform: "sqs", Instance: "catalogue-target-response-sqs",
			Operation: "consume catalogue-target-response-sqs",
			Details:   map[string]any{"queue": "catalogue-target-response-sqs"},
		}},
		{BaseEntity: model.BaseEntity{
			ID: "llm", Type: "queue_consumer", Name: "target-calculation-events-sqs-consumer",
			Platform: "sqs", Instance: "target-calculation-events-sqs-consumer",
			Operation: "consume target-calculation-events-sqs-consumer",
			Details: map[string]any{
				"queue": "services.aws.sqs.catalog-target-response-sqs.url (default local URL follows)",
			},
		}},
	}

	discoverystage.StampInstanceRefs(idx, exposures, nil)
	out := reconcile.DedupeExposures(exposures)
	if len(out) != 1 {
		t.Fatalf("stamp then dedupe should collapse physical queue variants, got %d", len(out))
	}
}
