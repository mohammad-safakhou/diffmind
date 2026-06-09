package eval

import (
	"context"
	"fmt"
	"sort"

	"github.com/mohammad-safakhou/diffmind/internal/agents"
	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// floor_coverage.go answers "what fraction of what the full LLM run found could
// the FREE deterministic floor have found?" per objective. It is the dial for
// the cost lever: as the floor grows (Workstream F), this rises and LLM work can
// shrink.
//
// IMPORTANT: this is COVERAGE, not recall. The LLM run is NOT ground truth, so
// floor∩llm / llm measures floor↔LLM overlap, not correctness. True floor recall
// needs human labels (|floor ∩ labels| / |labels|) and is a separate metric.

// FloorCoverage is the floor↔LLM overlap for one objective.
type FloorCoverage struct {
	Objective  string  `json:"objective"`
	FloorKeys  int     `json:"floor_keys"`         // distinct identity keys the floor produced
	LLMKeys    int     `json:"llm_keys"`           // distinct identity keys the LLM run produced
	Covered    int     `json:"covered"`            // |floor ∩ llm|
	FloorOnly  int     `json:"floor_only"`         // floor keys absent from the LLM run (floor FP or LLM miss)
	Coverage   float64 `json:"coverage"`           // Covered/LLMKeys when Applicable, else 0
	Applicable bool    `json:"applicable"`         // false when LLMKeys == 0 (coverage is N/A, never 1.0)
}

// FloorCoverageReport is the per-objective coverage of one repo's floor against
// one finished LLM run.
type FloorCoverageReport struct {
	Repo       string          `json:"repo"`
	RunID      string          `json:"run_id,omitempty"`
	Objectives []FloorCoverage `json:"objectives"`
}

// computeFloorCoverage compares the deterministic floor's identity-key sets
// against the LLM run's, per objective (and the connections pseudo-objective),
// using the same identity the scorer/dedup use. Pure and table-testable.
func computeFloorCoverage(floor, llm Extracted) []FloorCoverage {
	fk := keysByObjective(floor)
	lk := keysByObjective(llm)

	objSet := map[string]struct{}{}
	for t := range fk {
		objSet[t] = struct{}{}
	}
	for t := range lk {
		objSet[t] = struct{}{}
	}
	objs := make([]string, 0, len(objSet))
	for t := range objSet {
		objs = append(objs, t)
	}
	sort.Strings(objs)

	out := make([]FloorCoverage, 0, len(objs))
	for _, t := range objs {
		floorSet := toSet(fk[t])
		llmSet := toSet(lk[t])
		covered := intersectionCount(floorSet, llmSet)
		fc := FloorCoverage{
			Objective:  t,
			FloorKeys:  len(floorSet),
			LLMKeys:    len(llmSet),
			Covered:    covered,
			FloorOnly:  len(floorSet) - covered,
			Applicable: len(llmSet) > 0,
		}
		if fc.Applicable {
			fc.Coverage = float64(covered) / float64(len(llmSet))
		}
		out = append(out, fc)
	}
	return out
}

// RunFloorCoverage builds the deterministic floor for repo and compares it to a
// finished LLM run's artifacts. No LLM is invoked for the floor; the run's
// artifacts are read from disk. Caveat: if repo has changed since the run, the
// comparison is approximate — this is an operational signal, not a gate.
func RunFloorCoverage(ctx context.Context, repo, runDir string, cfg config.Config) (FloorCoverageReport, error) {
	idx, err := astpkg.Build(ctx, repo, "", cfg.Runtime.Workers, nil)
	if err != nil {
		return FloorCoverageReport{}, fmt.Errorf("ast build %s: %w", repo, err)
	}
	res := agents.DeterministicFloor(ctx, idx, repo, cfg)
	floor := Extracted{Exposures: res.Exposures, Dependencies: res.Dependencies, Connections: res.Connections}
	llm, err := LoadRunArtifacts(runDir)
	if err != nil {
		return FloorCoverageReport{}, err
	}
	return FloorCoverageReport{Repo: repo, Objectives: computeFloorCoverage(floor, llm)}, nil
}

func toSet(keys []string) map[string]struct{} {
	s := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		s[k] = struct{}{}
	}
	return s
}

func intersectionCount(a, b map[string]struct{}) int {
	// iterate the smaller set
	if len(b) < len(a) {
		a, b = b, a
	}
	n := 0
	for k := range a {
		if _, ok := b[k]; ok {
			n++
		}
	}
	return n
}
