package agents

import "testing"

func TestSeedStructurallyUnverifiable(t *testing.T) {
	// Evidence-backed seed: verifiable, must NOT be deletable on a single "no".
	good := llmEntity{Type: "http_route", Name: "GET /a", Locations: []llmLocation{{File: "A.java", StartLine: 10}}}
	if seedStructurallyUnverifiable(good) {
		t.Error("evidence-backed seed wrongly flagged unverifiable")
	}
	// No location -> unverifiable -> deletion corroborated.
	if !seedStructurallyUnverifiable(llmEntity{Type: "http_route", Name: "GET /a"}) {
		t.Error("seed with no location should be unverifiable")
	}
	// Missing name/type -> unverifiable.
	if !seedStructurallyUnverifiable(llmEntity{Locations: []llmLocation{{File: "A.java"}}}) {
		t.Error("seed missing name/type should be unverifiable")
	}
	// Location present but blank file -> unverifiable.
	if !seedStructurallyUnverifiable(llmEntity{Type: "x", Name: "y", Locations: []llmLocation{{File: "  "}}}) {
		t.Error("blank-file location should not count as verifiable")
	}
}

func TestDowngradeConfidence(t *testing.T) {
	if got := downgradeConfidence(1.0, 0.7); got != 0.7 {
		t.Errorf("1.0*0.7=0.7 >= floor, want 0.7 got %v", got)
	}
	// 0.8*0.7 = 0.56 < floor 0.7 -> clamp to floor (retain through the gate).
	if got := downgradeConfidence(0.8, 0.7); got != 0.7 {
		t.Errorf("want clamp to floor 0.7, got %v", got)
	}
}

func TestAppendUniqueTag(t *testing.T) {
	tags := appendUniqueTag(nil, "reexamination_doubted")
	tags = appendUniqueTag(tags, "reexamination_doubted")
	if len(tags) != 1 {
		t.Errorf("tag should not duplicate: %v", tags)
	}
}
