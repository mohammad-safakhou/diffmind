package connections

import (
	"context"
	"strings"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func repairFixture() (RepairInput, map[string]any) {
	idx := &astpkg.ProjectIndex{
		Files:   map[string]*astpkg.FileAST{"src/Job.java": {Path: "src/Job.java"}},
		Configs: map[string]*astpkg.ConfigFile{"application.yml": {Path: "application.yml"}},
	}
	in := RepairInput{
		Index: idx,
		Exposures: []model.Exposure{
			{BaseEntity: model.BaseEntity{ID: "exp-cron", Type: "scheduled_job", Name: "nightly sync"}},
			{BaseEntity: model.BaseEntity{ID: "exp-route", Type: "http_route", Name: "GET /health"}},
		},
		Dependencies: []model.Dependency{
			{BaseEntity: model.BaseEntity{ID: "dep-db", Type: "db_operation", Name: "write orders"}},
		},
		Connections: []model.Connection{
			{FromExposureID: "exp-route", ToDependencyID: "dep-db"}, // route already connected
		},
	}
	goodEvidence := []any{map[string]any{"file": "src/Job.java", "start_line": 12, "snippet": "repo.save(...)"}}
	payload := map[string]any{"connections": []any{
		map[string]any{ // valid: dangling exposure, known dep, real evidence
			"from_exposure_id": "exp-cron", "to_dependency_id": "dep-db",
			"confidence": 0.95, "evidence": goodEvidence,
		},
		map[string]any{ // hallucinated dependency id
			"from_exposure_id": "exp-cron", "to_dependency_id": "dep-invented",
			"confidence": 0.9, "evidence": goodEvidence,
		},
		map[string]any{ // evidence cites a file outside the index
			"from_exposure_id": "exp-cron", "to_dependency_id": "dep-db",
			"confidence": 0.9, "evidence": []any{map[string]any{"file": "made/Up.java", "start_line": 3}},
		},
		map[string]any{ // below the confidence minimum
			"from_exposure_id": "exp-cron", "to_dependency_id": "dep-db",
			"confidence": 0.2, "evidence": goodEvidence,
		},
	}}
	return in, payload
}

func TestRepairRunnerValidatesProposals(t *testing.T) {
	in, payload := repairFixture()
	var gotPrompt string
	r := RepairRunner{
		MinConfidence: 0.7,
		Prompt: func(_ context.Context, _ string, prompt string, _ map[string]any) (map[string]any, error) {
			gotPrompt = prompt
			return payload, nil
		},
	}
	out, err := r.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Dangling != 1 {
		t.Errorf("want 1 dangling exposure (route is connected), got %d", out.Dangling)
	}
	if len(out.Connections) != 1 {
		t.Fatalf("want exactly the valid proposal accepted, got %d: %+v", len(out.Connections), out.Connections)
	}
	c := out.Connections[0]
	if c.Source != model.ConnectionSourceLLMRepair {
		t.Errorf("repaired connection must carry llm_repair provenance, got %q", c.Source)
	}
	if c.Confidence != repairConfidenceCeiling {
		t.Errorf("confidence must be clamped to %.2f, got %.2f", repairConfidenceCeiling, c.Confidence)
	}
	if len(out.Rejected) != 3 {
		t.Errorf("want 3 rejected proposals, got %d: %+v", len(out.Rejected), out.Rejected)
	}
	// The prompt offers only the dangling exposure, never the connected one.
	if !strings.Contains(gotPrompt, "exp-cron") || strings.Contains(gotPrompt, "GET /health") {
		t.Errorf("prompt should list only dangling exposures:\n%s", gotPrompt)
	}
}

func TestRepairRunnerSkipsWhenNothingDangles(t *testing.T) {
	in, _ := repairFixture()
	in.Connections = append(in.Connections, model.Connection{FromExposureID: "exp-cron", ToDependencyID: "dep-db"})
	called := false
	r := RepairRunner{
		MinConfidence: 0.7,
		Prompt: func(_ context.Context, _ string, _ string, _ map[string]any) (map[string]any, error) {
			called = true
			return map[string]any{"connections": []any{}}, nil
		},
	}
	out, err := r.Run(context.Background(), in)
	if err != nil || called || len(out.Connections) != 0 || out.Dangling != 0 {
		t.Errorf("fully-connected run must not invoke the LLM (called=%v, out=%+v, err=%v)", called, out, err)
	}
}

