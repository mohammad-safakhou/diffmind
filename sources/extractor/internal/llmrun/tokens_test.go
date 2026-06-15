package llmrun

import "testing"

func TestTokenTotalsRecordsStageJobAndRunTotals(t *testing.T) {
	var totals TokenTotals
	first := totals.Record("discover.exposure.http_route", SessionState{
		Input: 100, Output: 20, Reasoning: 5, CacheRead: 300, Cost: 0.01,
	})
	totals.Record("discover.dependency.db_operation", SessionState{
		Input: 50, Output: 10, Reasoning: 2, CacheWrite: 40, Cost: 0.02,
	})

	if first.Calls != 1 || first.Total() != 125 {
		t.Fatalf("first call bucket = %+v", first)
	}
	stage := totals.Stage("discovery")
	if stage == nil || stage.Calls != 2 || stage.Total() != 187 {
		t.Fatalf("discovery bucket = %+v", stage)
	}
	all := totals.All()
	if all["total"].Total != 187 || all["discovery"].Calls != 2 {
		t.Fatalf("all totals = %+v", all)
	}
}

func TestStageFromJob(t *testing.T) {
	tests := map[string]string{
		"repo_facts":                       "repo_facts",
		"discover.exposure.http_route":     "discovery",
		"reexamine.exposure.http_route.id": "reexamination",
		"detail.exposure.http_route.id":    "other", // detail stage removed → unmapped
		"connections.batch.1":              "connections",
		"unknown.job":                      "other",
		"":                                 "other",
	}
	for jobID, want := range tests {
		if got := StageFromJob(jobID); got != want {
			t.Errorf("StageFromJob(%q) = %q, want %q", jobID, got, want)
		}
	}
}
