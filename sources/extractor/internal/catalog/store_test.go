package catalog

import (
	"errors"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestStoreManualEditSurvivesLaterAutomationImport(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time {
		now = now.Add(time.Minute)
		return now
	}

	first, summary, err := store.Import(ImportInput{
		RunID: "run-1",
		Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
			ID: "run-e1", Type: "http_route", Name: "GET /orders", Service: "orders", Summary: "generated",
			Details: map[string]any{"method": "GET", "path": "/orders"},
		}}},
		Dependencies: []model.Dependency{{BaseEntity: model.BaseEntity{
			ID: "run-d1", Type: "db_operation", Name: "read orders", Service: "orders", Summary: "generated",
			Platform: "postgres", Details: map[string]any{"table": "orders", "operation": "read"},
		}}},
		Connections: []model.Connection{{
			ID: "run-c1", FromExposureID: "run-e1", ToDependencyID: "run-d1", PathSignature: "orders->db",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Added != 3 {
		t.Fatalf("added = %d, want 3", summary.Added)
	}

	first.Exposures[0].Summary = "curated by an architect"
	manual, err := store.SaveManual(first)
	if err != nil {
		t.Fatal(err)
	}
	if got := manual.Records[manual.Exposures[0].ID].Owner; got != OwnerManual {
		t.Fatalf("owner = %q, want manual", got)
	}

	second, summary, err := store.Import(ImportInput{
		RunID: "run-2",
		Exposures: []model.Exposure{{BaseEntity: model.BaseEntity{
			ID: "other-e", Type: "http_route", Name: "GET /orders", Service: "orders", Summary: "new generated text",
			Details: map[string]any{"method": "GET", "path": "/orders"},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.SkippedManual != 1 {
		t.Fatalf("skipped_manual = %d, want 1", summary.SkippedManual)
	}
	if got := second.Exposures[0].Summary; got != "curated by an architect" {
		t.Fatalf("manual summary overwritten: %q", got)
	}
}

func TestStoreRejectsStaleRevisionAndOrphanConnection(t *testing.T) {
	store := NewStore(t.TempDir())
	doc, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	doc.Exposures = append(doc.Exposures, model.Exposure{BaseEntity: model.BaseEntity{
		ID: "e1", Type: "http_route", Name: "GET /orders",
	}})
	saved, err := store.SaveManual(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Records["e1"].Owner; got != OwnerManual {
		t.Fatalf("owner = %q, want manual", got)
	}
	if _, err := store.SaveManual(doc); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale save error = %v, want revision conflict", err)
	}

	saved.Connections = append(saved.Connections, model.Connection{
		ID: "c1", FromExposureID: "e1", ToDependencyID: "missing",
	})
	if _, err := store.SaveManual(saved); err == nil {
		t.Fatal("expected orphan connection validation error")
	}
}

func TestNormalizeServiceNameCollapsesPath(t *testing.T) {
	cases := map[string]string{
		"/Users/x/repos/payments":  "payments",
		"/Users/x/repos/payments/": "payments",
		"payments":                 "payments",
		"  payments  ":             "payments",
		"":                         "",
	}
	for in, want := range cases {
		if got := NormalizeServiceName(in); got != want {
			t.Errorf("NormalizeServiceName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A run stores the repo's absolute path as the service; a hand-authored file
// names it plainly. The catalog identity must treat them as one service, or the
// run's facts import as duplicates of the matching authored facts.
func TestEntityCatalogKeyMatchesRunAndFileService(t *testing.T) {
	details := map[string]any{"table": "campaign_item", "operation": "read", "platform": "postgres"}
	runFact := model.BaseEntity{Type: "db_operation", Name: "read campaign_item", Service: "/Users/x/repos/routing-service", Platform: "postgres", Details: details}
	fileFact := model.BaseEntity{Type: "db_operation", Name: "read campaign_item", Service: "routing-service", Platform: "postgres", Details: details}
	if run, file := EntityCatalogKey("dependency", runFact), EntityCatalogKey("dependency", fileFact); run != file {
		t.Fatalf("run vs file identity must collapse:\n run  = %s\n file = %s", run, file)
	}
}
