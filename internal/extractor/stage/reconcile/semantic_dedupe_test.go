package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
)

func TestDedupeExposuresSemanticPrefersSpecificType(t *testing.T) {
	in := []model.Exposure{
		{BaseEntity: model.BaseEntity{ID: "cli", Type: "cli_command", Name: "Campaign listener", Platform: "sqs", Instance: "campaign-events", Operation: "consume campaign-events", Locations: []model.Location{{File: "Listener.java", StartLine: 10}}}},
		{BaseEntity: model.BaseEntity{ID: "queue", Type: "queue_consumer", Name: "Campaign listener", Platform: "sqs", Instance: "campaign-events", Operation: "consume campaign-events", Locations: []model.Location{{File: "Listener.java", StartLine: 10}}}},
	}
	out := DedupeExposures(in)
	if len(out) != 1 {
		t.Fatalf("expected one deduped exposure, got %d", len(out))
	}
	if out[0].Type != "queue_consumer" {
		t.Fatalf("type = %q, want queue_consumer", out[0].Type)
	}
}

func TestDedupeDependenciesSemanticPrefersSpecificType(t *testing.T) {
	in := []model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "cmd", Type: "command_exec", Name: "AWS SQS Message Publishing", Platform: "sqs", Instance: "target-request", Operation: "publish target-request", Locations: []model.Location{{File: "Publisher.java", StartLine: 20}}}},
		{BaseEntity: model.BaseEntity{ID: "queue", Type: "queue_publish", Name: "target-request", Platform: "sqs", Instance: "target-request", Operation: "publish target-request", Locations: []model.Location{{File: "Publisher.java", StartLine: 20}}}},
	}
	out := DedupeDependencies(in)
	if len(out) != 1 {
		t.Fatalf("expected one deduped dependency, got %d", len(out))
	}
	if out[0].Type != "queue_publish" {
		t.Fatalf("type = %q, want queue_publish", out[0].Type)
	}
}

func TestDedupeExposuresSemanticPrefersScheduledJobOverCronCliDuplicate(t *testing.T) {
	in := []model.Exposure{
		{BaseEntity: model.BaseEntity{ID: "cli", Type: "cli_command", Name: "target-calc", Platform: "process", Instance: "target-calc", Operation: "target-calc", Locations: []model.Location{{File: ".example/config/values.yaml", StartLine: 1}}}},
		{BaseEntity: model.BaseEntity{ID: "job", Type: "scheduled_job", Name: "target-calc", Platform: "scheduler", Instance: "0 2 * * *", Operation: "target-calc", Locations: []model.Location{{File: ".example/config/values.yaml", StartLine: 1}}}},
	}
	out := DedupeExposures(in)
	if len(out) != 1 {
		t.Fatalf("expected one deduped exposure, got %d", len(out))
	}
	if out[0].Type != "scheduled_job" {
		t.Fatalf("type = %q, want scheduled_job", out[0].Type)
	}
}

func TestDedupeDependenciesDropsSqsOutboundTransportWhenQueuePublishExists(t *testing.T) {
	in := []model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "http", Type: "outbound_http", Name: "Amazon SQS", Platform: "http", Instance: "Amazon SQS", Operation: "SendMessage", Locations: []model.Location{{File: "AwsSqsConfig.java", StartLine: 1}}}},
		{BaseEntity: model.BaseEntity{ID: "queue", Type: "queue_publish", Name: "catalogue-target-request-sqs", Platform: "sqs", Instance: "catalogue-target-request-sqs", Operation: "publish catalogue-target-request-sqs", Locations: []model.Location{{File: "TargetCalculationRequestEventPublisher.java", StartLine: 31}}}},
	}
	out := DedupeDependencies(in)
	if len(out) != 1 {
		t.Fatalf("expected one dependency after transport demotion, got %d", len(out))
	}
	if out[0].Type != "queue_publish" {
		t.Fatalf("type = %q, want queue_publish", out[0].Type)
	}
}

func TestDedupeDependenciesDropsAthenaOutboundTransportWhenDBOperationExists(t *testing.T) {
	in := []model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "http", Type: "outbound_http", Name: "Amazon Athena API", Platform: "http", Instance: "Amazon Athena", Operation: "StartQueryExecution", Locations: []model.Location{{File: "AthenaConfig.java", StartLine: 1}}}},
		{BaseEntity: model.BaseEntity{ID: "db", Type: "db_operation", Name: "AthenaQueryExecutor.runQuery", Platform: "athena", Instance: "athena", Operation: "SELECT", Locations: []model.Location{{File: "AthenaQueryExecutor.java", StartLine: 20}}}},
	}
	out := DedupeDependencies(in)
	if len(out) != 1 {
		t.Fatalf("expected one dependency after transport demotion, got %d", len(out))
	}
	if out[0].Type != "db_operation" {
		t.Fatalf("type = %q, want db_operation", out[0].Type)
	}
}
