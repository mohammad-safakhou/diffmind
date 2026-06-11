package reconcile

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// Item 6: embedded-verb method names must fold to read/write.
func TestNormalizeDBOpEmbeddedVerb(t *testing.T) {
	cases := map[string]string{
		"hardDeleteAllSubTargets": "write",
		"batchInsertRows":         "write",
		"findByCampaignId":        "read",
		"SELECT":                  "read",
		"saveAll":                 "write",
		"nextLocalId":             "nextlocalid", // genuinely opaque -> passthrough
		"unknown":                 "unknown",
	}
	for in, want := range cases {
		if got := normalizeDBOp(in); got != want {
			t.Errorf("normalizeDBOp(%q)=%q want %q", in, got, want)
		}
	}
}

// Item 5: a queue and its "-consumer" variant must share one identity.
func TestQueueConsumerSuffixCollapses(t *testing.T) {
	mk := func(id, name string) model.Exposure {
		return model.Exposure{BaseEntity: model.BaseEntity{
			ID: id, Type: "queue_consumer", Name: name, Platform: "sqs",
			Details: map[string]any{"queue": name},
		}}
	}
	out := DedupeExposures([]model.Exposure{
		mk("1", "ats-salesforce-data-events-sqs"),
		mk("2", "ats-salesforce-data-events-sqs-consumer"),
	})
	if len(out) != 1 {
		t.Fatalf("queue + -consumer variant should collapse to 1, got %d: %+v", len(out), names(out))
	}
}

// Item 4: a scheduled_job and a cli_command in the same file collapse to the job.
func TestJobEntrypointCollapse(t *testing.T) {
	loc := []model.Location{{File: "svc/CampaignDeliveredQuantityJob.java", StartLine: 19}}
	cliLoc := []model.Location{{File: "svc/CampaignDeliveredQuantityJob.java", StartLine: 13}}
	mainLoc := []model.Location{{File: "svc/Application.java", StartLine: 17}}
	out := DedupeExposures([]model.Exposure{
		{BaseEntity: model.BaseEntity{ID: "1", Type: "scheduled_job", Name: "CampaignDeliveredQuantityJob.run", Locations: loc}},
		{BaseEntity: model.BaseEntity{ID: "2", Type: "cli_command", Name: "campaign-delivered-quantity-job", Locations: cliLoc}},
		{BaseEntity: model.BaseEntity{ID: "3", Type: "cli_command", Name: "Application main launcher", Locations: mainLoc}},
	})
	var sched, cli int
	for _, e := range out {
		switch e.Type {
		case "scheduled_job":
			sched++
		case "cli_command":
			cli++
		}
	}
	if sched != 1 {
		t.Errorf("expected 1 scheduled_job, got %d", sched)
	}
	if cli != 1 { // only the standalone main launcher survives
		t.Errorf("expected only the standalone cli_command to survive, got %d: %+v", cli, names(out))
	}
}

// Item 7: a db_operation on a sequence (junk) is dropped, even from LLM output.
func TestJunkDataResourceDropped(t *testing.T) {
	out := DedupeDependencies([]model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "1", Type: "db_operation", Name: "read orders", Details: map[string]any{"table": "orders", "operation": "read"}}},
		{BaseEntity: model.BaseEntity{ID: "2", Type: "db_operation", Name: "nextLocalId", Details: map[string]any{"table": "traffic_configuration_id_seq", "operation": "nextLocalId"}}},
	})
	if len(out) != 1 {
		t.Fatalf("the sequence db_op should be dropped, got %d: %+v", len(out), names2(out))
	}
	if dataResource(out[0].BaseEntity) != "order" {
		t.Errorf("surviving dep should be orders, got %q", dataResource(out[0].BaseEntity))
	}
}

// Item 9: a db_operation named like a caller symbol is rewritten to the data fact.
func TestDataNameCanonicalized(t *testing.T) {
	out := DedupeDependencies([]model.Dependency{
		{BaseEntity: model.BaseEntity{ID: "1", Type: "db_operation",
			Name:    "AthenaDebugController.campaignDeliveredQuantityDebug",
			Details: map[string]any{"table": "agg_stats", "operation": "select"}}},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(out))
	}
	if out[0].Name != "read agg_stats" {
		t.Errorf("name should be canonicalized to 'read agg_stats', got %q", out[0].Name)
	}
}

func TestNormalizeQueueDest(t *testing.T) {
	cases := map[string]string{
		"foo-sqs":          "foo-sqs",
		"foo-sqs-consumer": "foo-sqs",
		"Foo_LISTENER":     "foo",
		"bar-queue":        "bar-queue", // -queue is part of the real name, not stripped
	}
	for in, want := range cases {
		if got := NormalizeQueueDest(in); got != want {
			t.Errorf("NormalizeQueueDest(%q)=%q want %q", in, got, want)
		}
	}
}

func names(es []model.Exposure) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Type + ":" + e.Name
	}
	return out
}
func names2(ds []model.Dependency) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.Type + ":" + d.Name
	}
	return out
}
