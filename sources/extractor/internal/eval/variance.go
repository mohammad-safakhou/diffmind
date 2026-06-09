package eval

import (
	"math"
	"sort"
)

// variance.go measures run-to-run STABILITY of the extractor across K runs of
// the SAME repo. It is the proof tool behind every "this change stabilizes
// output" claim: run K times before and after, compare core/union.
//
// It deliberately reuses the scorer's identity functions — identityKey for
// entities and connectionPairKey for connections — so "stable" means "the same
// architectural facts run-to-run", identical to how the pipeline dedups and how
// accuracy is scored. It never reaches into reconcile.SemanticKeyLoose directly.

// ObjectiveVariance captures stability for one objective (entity type) or the
// "connections" pseudo-objective across K runs.
type ObjectiveVariance struct {
	Objective string `json:"objective"`
	// Counts is the per-run entity count, in run order (so a 0 reveals a run
	// that found nothing for this objective).
	Counts      []int   `json:"counts"`
	Mean        float64 `json:"mean"`
	Stdev       float64 `json:"stdev"` // population stdev of Counts
	Min         int     `json:"min"`
	Max         int     `json:"max"`
	CoreKeys    int     `json:"core_keys"`    // identity keys present in ALL runs
	UnionKeys   int     `json:"union_keys"`   // identity keys present in ANY run
	CoreUnion   float64 `json:"core_union"`   // CoreKeys/UnionKeys; 1.0 = perfectly reproducible
	JaccardMean float64 `json:"jaccard_mean"` // mean pairwise Jaccard over per-run key sets
}

// VarianceReport is the K-run stability result for one repo.
type VarianceReport struct {
	Runs       int                 `json:"runs"`
	RunIDs     []string            `json:"run_ids,omitempty"`
	Objectives []ObjectiveVariance `json:"objectives"`
}

// Variance computes per-objective stability across K extractions of the same
// repo. Entities are bucketed by type and keyed by identityKey; connections by
// endpoint-identity pair. core/union is the headline number (1.0 = identical
// fact sets in every run); JaccardMean is the mean pairwise set overlap. Counts
// give raw count volatility (mean/stdev/min/max).
func Variance(runs []Extracted, runIDs []string) VarianceReport {
	rep := VarianceReport{Runs: len(runs), RunIDs: runIDs}
	if len(runs) == 0 {
		return rep
	}
	perRun := make([]map[string][]string, len(runs))
	objSet := map[string]struct{}{}
	for i, r := range runs {
		perRun[i] = keysByObjective(r)
		for t := range perRun[i] {
			objSet[t] = struct{}{}
		}
	}
	objs := make([]string, 0, len(objSet))
	for t := range objSet {
		objs = append(objs, t)
	}
	sort.Strings(objs)
	for _, t := range objs {
		rep.Objectives = append(rep.Objectives, objectiveVariance(t, perRun))
	}
	return rep
}

// keysByObjective returns, for ONE run, the identity keys of every entity keyed
// by objective type, plus connection endpoint-pair keys under "connections".
// Connection endpoints are resolved through this run's own id→key map, so an
// unresolved endpoint becomes "<unresolved>" exactly as the scorer would see it.
func keysByObjective(ext Extracted) map[string][]string {
	out := map[string][]string{}
	idToKey := make(map[string]string, len(ext.Exposures)+len(ext.Dependencies))
	for _, e := range ext.Exposures {
		k := identityKey(e.BaseEntity)
		out[e.Type] = append(out[e.Type], k)
		idToKey[e.ID] = k
	}
	for _, d := range ext.Dependencies {
		k := identityKey(d.BaseEntity)
		out[d.Type] = append(out[d.Type], k)
		idToKey[d.ID] = k
	}
	for _, c := range ext.Connections {
		out["connections"] = append(out["connections"],
			connectionPairKey(idToKey[c.FromExposureID], idToKey[c.ToDependencyID]))
	}
	return out
}

func objectiveVariance(obj string, perRun []map[string][]string) ObjectiveVariance {
	k := len(perRun)
	counts := make([]int, k)
	sets := make([]map[string]struct{}, k)
	for i := range perRun {
		keys := perRun[i][obj]
		counts[i] = len(keys)
		s := make(map[string]struct{}, len(keys))
		for _, key := range keys {
			s[key] = struct{}{}
		}
		sets[i] = s
	}
	mean, stdev, mn, mx := intStats(counts)
	core, union := coreUnion(sets)
	cu := 1.0
	if union > 0 {
		cu = float64(core) / float64(union)
	}
	return ObjectiveVariance{
		Objective:   obj,
		Counts:      counts,
		Mean:        mean,
		Stdev:       stdev,
		Min:         mn,
		Max:         mx,
		CoreKeys:    core,
		UnionKeys:   union,
		CoreUnion:   cu,
		JaccardMean: meanPairwiseJaccard(sets),
	}
}

func intStats(xs []int) (mean, stdev float64, min, max int) {
	if len(xs) == 0 {
		return 0, 0, 0, 0
	}
	min, max = xs[0], xs[0]
	sum := 0
	for _, x := range xs {
		sum += x
		if x < min {
			min = x
		}
		if x > max {
			max = x
		}
	}
	mean = float64(sum) / float64(len(xs))
	var ss float64
	for _, x := range xs {
		d := float64(x) - mean
		ss += d * d
	}
	stdev = math.Sqrt(ss / float64(len(xs))) // population stdev
	return mean, stdev, min, max
}

// coreUnion counts identity keys present in ALL runs (core) and in ANY run
// (union) across the per-run key sets.
func coreUnion(sets []map[string]struct{}) (core, union int) {
	if len(sets) == 0 {
		return 0, 0
	}
	runsWithKey := map[string]int{}
	for _, s := range sets {
		for key := range s {
			runsWithKey[key]++
		}
	}
	for _, c := range runsWithKey {
		union++
		if c == len(sets) {
			core++
		}
	}
	return core, union
}

// meanPairwiseJaccard averages the Jaccard overlap over every unordered pair of
// run key sets. With fewer than two runs there is no variance to measure, so it
// returns 1.0 (trivially reproducible).
func meanPairwiseJaccard(sets []map[string]struct{}) float64 {
	n := len(sets)
	if n < 2 {
		return 1.0
	}
	var sum float64
	pairs := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sum += jaccard(sets[i], sets[j])
			pairs++
		}
	}
	if pairs == 0 {
		return 1.0
	}
	return sum / float64(pairs)
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	inter := 0
	for key := range a {
		if _, ok := b[key]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 1.0
	}
	return float64(inter) / float64(union)
}
