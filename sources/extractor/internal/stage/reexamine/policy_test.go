package reexamine

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
)

func TestSeedStructurallyUnverifiable(t *testing.T) {
	// Evidence-backed seed: verifiable, must NOT be deletable on a single "no".
	good := llmEntity{Type: "http_route", Name: "GET /a", Locations: []extraction.Location{{File: "A.java", StartLine: 10}}}
	if SeedStructurallyUnverifiable(good) {
		t.Error("evidence-backed seed wrongly flagged unverifiable")
	}
	// No location -> unverifiable -> deletion corroborated.
	if !SeedStructurallyUnverifiable(llmEntity{Type: "http_route", Name: "GET /a"}) {
		t.Error("seed with no location should be unverifiable")
	}
	// Missing name/type -> unverifiable.
	if !SeedStructurallyUnverifiable(llmEntity{Locations: []extraction.Location{{File: "A.java"}}}) {
		t.Error("seed missing name/type should be unverifiable")
	}
	// Location present but blank file -> unverifiable.
	if !SeedStructurallyUnverifiable(llmEntity{Type: "x", Name: "y", Locations: []extraction.Location{{File: "  "}}}) {
		t.Error("blank-file location should not count as verifiable")
	}
}

func TestDowngradeConfidence(t *testing.T) {
	if got := DowngradeConfidence(1.0, 0.7); got != 0.7 {
		t.Errorf("1.0*0.7=0.7 >= floor, want 0.7 got %v", got)
	}
	// 0.8*0.7 = 0.56 < floor 0.7 -> clamp to floor (retain through the gate).
	if got := DowngradeConfidence(0.8, 0.7); got != 0.7 {
		t.Errorf("want clamp to floor 0.7, got %v", got)
	}
}

func TestAppendUniqueTag(t *testing.T) {
	tags := AppendUniqueTag(nil, "reexamination_doubted")
	tags = AppendUniqueTag(tags, "reexamination_doubted")
	if len(tags) != 1 {
		t.Errorf("tag should not duplicate: %v", tags)
	}
}
