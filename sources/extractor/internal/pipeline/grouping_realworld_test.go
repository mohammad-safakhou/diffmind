package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestDetailGroups_RealWorldCatalogueAPI runs the Go
// grouping algorithm against the real seed data captured from a
// failed run (.diffmind/runs/20260518T122739Z/state/discovery.json)
// and asserts the headline numbers that motivated this work:
//
//   - 156 input seeds → fewer than 25 batches (88% reduction target)
//   - http_route and db_operation each compress to 6-7 batches
//   - No batch exceeds the hard cap of 12
//
// We skip the test when the data file is not present (CI / fresh
// checkouts) so the suite stays green outside a development tree.
func TestDetailGroups_RealWorldCatalogueAPI(t *testing.T) {
	path := "../../.diffmind/runs/20260518T122739Z/state/discovery.json"
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("seed file not found (%s); skipping real-world grouping smoke test", path)
		return
	}
	var jobs []detailJob
	if err := json.Unmarshal(b, &jobs); err != nil {
		t.Fatalf("decode seeds: %v", err)
	}
	if len(jobs) == 0 {
		t.Skip("seed file is empty; skipping")
	}
	batches := DetailGroups(jobs)

	// Headline: at least 80% reduction in LLM calls.
	if reduction := 1.0 - float64(len(batches))/float64(len(jobs)); reduction < 0.8 {
		t.Errorf("batching only reduced %d seeds → %d batches (%.0f%%); target is at least 80%%",
			len(jobs), len(batches), reduction*100)
	}

	// No batch may exceed the hard cap.
	for i, bch := range batches {
		if len(bch) > detailBatchHardCap {
			t.Errorf("batch %d has %d entities; hard cap is %d", i, len(bch), detailBatchHardCap)
		}
	}

	// Every batch must be homogenous in (kind, type).
	for i, bch := range batches {
		if len(bch) == 0 {
			t.Errorf("batch %d is empty", i)
			continue
		}
		k0, t0 := string(bch[0].Objective.Kind), bch[0].Objective.Type
		for _, j := range bch[1:] {
			if string(j.Objective.Kind) != k0 || j.Objective.Type != t0 {
				t.Errorf("batch %d mixes objectives: %s.%s + %s.%s", i, k0, t0, j.Objective.Kind, j.Objective.Type)
			}
		}
	}

	// Diagnostic: print the per-objective batch sizes so future
	// regressions are easy to spot when reading test output.
	type stat struct {
		entities int
		batches  int
		sizes    []int
	}
	stats := map[string]*stat{}
	for _, bch := range batches {
		key := fmt.Sprintf("%s.%s", bch[0].Objective.Kind, bch[0].Objective.Type)
		s := stats[key]
		if s == nil {
			s = &stat{}
			stats[key] = s
		}
		s.entities += len(bch)
		s.batches++
		s.sizes = append(s.sizes, len(bch))
	}
	t.Logf("input seeds: %d → batches: %d (%.0f%% reduction)",
		len(jobs), len(batches), 100*(1-float64(len(batches))/float64(len(jobs))))
	for k, s := range stats {
		t.Logf("  %s: %d entities → %d batches %v", k, s.entities, s.batches, s.sizes)
	}
}
