package golden

import "testing"

func TestBuildSummarySortsCases(t *testing.T) {
	rep := corpusReport{
		Cases: []corpusCase{
			{Name: "b", Status: "passed", EntityCount: 2},
			{Name: "a", Status: "passed", EntityCount: 1},
		},
	}
	s := buildSummary(rep)
	if len(s.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d", len(s.Cases))
	}
	if s.Cases[0].Name != "a" || s.Cases[1].Name != "b" {
		t.Fatalf("cases are not sorted by name: %+v", s.Cases)
	}
}
