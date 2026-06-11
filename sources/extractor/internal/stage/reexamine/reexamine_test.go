package reexamine

import (
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

func TestRunnerReturnsEnrichedCandidate(t *testing.T) {
	out := (Policy{}).Run(Input{
		Objective: objectives.Objective{Kind: model.KindExposure, Type: "http_route"},
		Candidate: extraction.Candidate{
			Type: "http_route", Name: "GET /orders", Confidence: 0.9,
			Locations: []extraction.Location{{File: "api.go", StartLine: 1}},
		},
		MinConfidence: 0.7,
	})
	if out.Needed {
		t.Fatalf("unexpected reexamination: %+v", out)
	}
	if out.Candidate.Details["method"] != "GET" || out.Candidate.Details["path"] != "/orders" {
		t.Fatalf("candidate was not enriched: %+v", out.Candidate)
	}
}
