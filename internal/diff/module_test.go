package diff

import "diffmind/internal/bundleio"
import "testing"

func TestBuildReportDetectsAddedRemovedChanged(t *testing.T) {
	from := bundleio.Bundle{SnapshotID: "s1", Entities: []bundleio.Entity{
		{ID: "e1", Type: "Endpoint", NaturalKey: "GET|/a", Confidence: 0.9, Attributes: map[string]any{"path": "/a"}, EvidenceIDs: []string{"x"}},
		{ID: "r1", Type: "RuntimeUnit", NaturalKey: "go|main|cmd/main.go", Confidence: 0.9, Attributes: map[string]any{"file": "cmd/main.go"}, EvidenceIDs: []string{"y"}},
	}}
	to := bundleio.Bundle{SnapshotID: "s2", Entities: []bundleio.Entity{
		{ID: "e2", Type: "Endpoint", NaturalKey: "GET|/a", Confidence: 0.8, Attributes: map[string]any{"path": "/a"}, EvidenceIDs: []string{"x"}},
		{ID: "c1", Type: "ConfigKey", NaturalKey: "APP_ENV|os.Getenv", Confidence: 0.9, Attributes: map[string]any{"key": "APP_ENV"}, EvidenceIDs: []string{"z"}},
	}}

	r := BuildReport(from, to)
	if r.Added != 1 || r.Removed != 1 || r.Changed != 1 || r.Unchanged != 0 {
		t.Fatalf("unexpected counts: %+v", r)
	}
	if r.ByType["ConfigKey"].Added != 1 {
		t.Fatalf("expected ConfigKey added")
	}
	if r.ByType["RuntimeUnit"].Removed != 1 {
		t.Fatalf("expected RuntimeUnit removed")
	}
	if r.ByType["Endpoint"].Changed != 1 {
		t.Fatalf("expected Endpoint changed")
	}
}
