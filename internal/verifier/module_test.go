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
	out, report, queue, err := verify(in, Options{
		PromoteThreshold: 0.9,
		DisputeThreshold: 0.7,
		StrictEvidence:   true,
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
	if report.ReviewQueueItems != 3 || len(queue) != 3 {
		t.Fatalf("expected 3 review queue items, got report=%d queue=%d", report.ReviewQueueItems, len(queue))
	}

	seenDecision := 0
	seenQueue := 0
	for _, e := range out.Entities {
		if e.Type == "VerificationDecision" {
			seenDecision++
			if len(e.EvidenceIDs) == 0 {
				t.Fatalf("decision entity must preserve evidence ids")
			}
			continue
		}
		if e.Type == "VerificationReviewQueue" {
			seenQueue++
			continue
		}
		if _, ok := e.Attributes["verification_status"]; !ok {
			t.Fatalf("entity missing verification status: %+v", e)
		}
	}
	if seenDecision != 3 {
		t.Fatalf("expected 3 verification decisions in output, got %d", seenDecision)
	}
	if seenQueue != 1 {
		t.Fatalf("expected one review queue entity in output, got %d", seenQueue)
	}
}

func TestVerifyDisputesCriticalClaimWithoutEvidenceWhenStrictEnabled(t *testing.T) {
	in := consolidation.IntelligenceBundle{
		SnapshotID: "s2",
		Entities: []consolidation.Entity{
			{ID: "e1", Type: "Endpoint", NaturalKey: "in|GET|/orders", Attributes: map[string]any{}, Confidence: 0.95},
			{ID: "e2", Type: "ConfigKey", NaturalKey: "app.name", Attributes: map[string]any{}, Confidence: 0.95},
		},
	}
	out, report, queue, err := verify(in, Options{
		PromoteThreshold: 0.9,
		DisputeThreshold: 0.7,
		StrictEvidence:   true,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.MissingEvidenceCritical != 1 {
		t.Fatalf("expected one missing critical evidence issue, got %d", report.MissingEvidenceCritical)
	}
	if len(queue) != 1 {
		t.Fatalf("expected one queue item, got %d", len(queue))
	}
	if queue[0].EntityID != "e1" || queue[0].Priority != "p1" {
		t.Fatalf("unexpected review queue item: %+v", queue[0])
	}
	statusByID := map[string]string{}
	reasonByID := map[string]string{}
	for _, e := range out.Entities {
		if e.Type == "VerificationDecision" || e.Type == "VerificationReviewQueue" {
			continue
		}
		statusByID[e.ID] = e.Attributes["verification_status"].(string)
		reasonByID[e.ID] = e.Attributes["verification_reason"].(string)
	}
	if statusByID["e1"] != "disputed" {
		t.Fatalf("expected e1 to be disputed, got %q", statusByID["e1"])
	}
	if statusByID["e2"] != "verified" {
		t.Fatalf("expected e2 to remain verified, got %q", statusByID["e2"])
	}
	if reasonByID["e1"] != "critical claim missing evidence ids" {
		t.Fatalf("unexpected reason for e1: %q", reasonByID["e1"])
	}
}

func TestVerifyTwoPassContradictionDisputesHighConfidenceEntity(t *testing.T) {
	in := consolidation.IntelligenceBundle{
		SnapshotID: "s3",
		Entities: []consolidation.Entity{
			{
				ID:         "ep1",
				Type:       "Endpoint",
				NaturalKey: "in|GET|/orders",
				Attributes: map[string]any{},
				EvidenceIDs: []string{
					"ev1",
				},
				Confidence: 0.96,
			},
			{
				ID:         "c1",
				Type:       "Conflict",
				NaturalKey: "Endpoint|in|GET|/orders|method",
				Attributes: map[string]any{
					"entity_type":        "Endpoint",
					"entity_natural_key": "in|GET|/orders",
				},
				EvidenceIDs: []string{
					"ev-conflict",
				},
				Confidence: 0.55,
			},
		},
	}
	out, report, queue, err := verify(in, Options{
		PromoteThreshold: 0.9,
		DisputeThreshold: 0.7,
		StrictEvidence:   true,
		TwoPass:          true,
	})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.ContradictionDisputes != 1 {
		t.Fatalf("expected one contradiction dispute, got %d", report.ContradictionDisputes)
	}
	statusByID := map[string]string{}
	reasonByID := map[string]string{}
	for _, e := range out.Entities {
		if e.Type == "VerificationDecision" || e.Type == "VerificationReviewQueue" {
			continue
		}
		statusByID[e.ID] = e.Attributes["verification_status"].(string)
		reasonByID[e.ID] = e.Attributes["verification_reason"].(string)
	}
	if statusByID["ep1"] != "disputed" {
		t.Fatalf("expected ep1 to be disputed by contradiction pass, got %q", statusByID["ep1"])
	}
	if reasonByID["ep1"] != "contradiction pass flagged subject from conflict entity" {
		t.Fatalf("unexpected contradiction reason: %q", reasonByID["ep1"])
	}
	if len(queue) < 2 {
		t.Fatalf("expected queue items for endpoint and conflict, got %d", len(queue))
	}
}
