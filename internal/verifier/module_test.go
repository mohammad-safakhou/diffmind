package verifier

import (
	"testing"

	"diffmind/internal/consolidation"
)

func TestVerifyClassifiesAndAddsDecisions(t *testing.T) {
	in := consolidation.IntelligenceBundle{
		SnapshotID: "s1",
		Entities: []consolidation.Entity{
			{ID: "e-high", Type: "Endpoint", NaturalKey: "in|GET|/a", Attributes: map[string]any{}, Confidence: 0.95, EvidenceIDs: []string{"ev1"}},
			{ID: "e-mid", Type: "Dependency", NaturalKey: "npm|axios|^1", Attributes: map[string]any{}, Confidence: 0.8, EvidenceIDs: []string{"ev2"}},
			{ID: "e-low", Type: "ConfigKey", NaturalKey: "X", Attributes: map[string]any{}, Confidence: 0.5, EvidenceIDs: []string{"ev3"}},
			{ID: "e-conflict", Type: "Conflict", NaturalKey: "ExternalCall|k", Attributes: map[string]any{}, Confidence: 0.6, EvidenceIDs: []string{"ev4"}},
		},
	}
	out, report, err := verify(in, Options{
		PromoteThreshold: 0.9,
		DisputeThreshold: 0.7,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.VerifiedCount != 1 || report.NeedsReviewCount != 1 || report.DisputedCount != 2 {
		t.Fatalf("unexpected verification counters: %+v", report)
	}
	if report.DecisionEntitiesAdded != 3 {
		t.Fatalf("expected 3 decision entities, got %d", report.DecisionEntitiesAdded)
	}

	seenDecision := 0
	for _, e := range out.Entities {
		if e.Type == "VerificationDecision" {
			seenDecision++
			if len(e.EvidenceIDs) == 0 {
				t.Fatalf("decision entity must preserve evidence ids")
			}
			continue
		}
		if _, ok := e.Attributes["verification_status"]; !ok {
			t.Fatalf("entity missing verification status: %+v", e)
		}
	}
	if seenDecision != 3 {
		t.Fatalf("expected 3 verification decisions in output, got %d", seenDecision)
	}
}
