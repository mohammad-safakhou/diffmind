package agents

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Detail-stage batch sizing. We treat the soft target as the natural
// max once a batch has accumulated a sensible cluster; the hard cap
// is a safety belt against an entire repo's worth of related
// entities collapsing into one giant prompt.
//
// Values chosen empirically from a real run (checkout-service):
//
//	156 seeds → 19 batches with cap=12 (88% reduction in LLM calls)
//
// See pipeline.go's detailGroups() for the call site. Both values
// are package-private constants on purpose: changing them is a
// real cost / quality tradeoff that should be made deliberately,
// not via a config knob the operator might set blindly.
const (
	detailBatchSoftTarget = 8
	detailBatchHardCap    = 12
)

// detailGroups partitions a set of detail jobs into batches such that:
//
//  1. Every batch contains only jobs from the SAME objective
//     (kind+type). Mixing http_route and queue_consumer in one
//     prompt would force the model to switch frames mid-call.
//
//  2. Within an objective, jobs are clustered by affinity: shared
//     source-file directory, shared source file, shared name prefix,
//     and overlapping name tokens all push two jobs into the same
//     batch.
//
//  3. Each batch is capped at detailBatchHardCap entities. Adding
//     to a batch past detailBatchSoftTarget incurs a penalty so we
//     prefer starting a fresh batch when affinity is weak.
//
// The result is a deterministic, semantically-coherent batching
// schedule. Two reasons to prefer this over fixed-size batches:
//
//   - Token economy: the shared preamble (repo facts, instructions,
//     objective context) is amortised across the batch, AND the
//     model can `read` each shared source file once and answer
//     multiple entities from it instead of re-reading per entity.
//
//   - Quality: a batch of 10 entities in the same Spring controller
//     gives the model a coherent context. A batch of 10 random
//     entities forces it to context-switch, drop fields, or rush.
//
// The algorithm is O(n²) over the seeds in each objective bucket.
// That is fine: n is small (typical bucket sizes 1-70) and we run
// this once per run, not per LLM call.
func detailGroups(jobs []detailJob) [][]detailJob {
	if len(jobs) == 0 {
		return nil
	}
	// Bucket by (objective.Kind, objective.Type).
	type bucketKey struct{ kind, typ string }
	buckets := map[bucketKey][]detailJob{}
	keys := []bucketKey{} // insertion order for deterministic output
	for _, j := range jobs {
		k := bucketKey{string(j.Objective.Kind), j.Objective.Type}
		if _, seen := buckets[k]; !seen {
			keys = append(keys, k)
		}
		buckets[k] = append(buckets[k], j)
	}
	// Stable order within each bucket so two runs produce the same
	// groupings (and the dashboard's "compare runs" surface stays
	// sane).
	for _, k := range keys {
		sort.SliceStable(buckets[k], func(i, jj int) bool {
			return buckets[k][i].Seed.Name < buckets[k][jj].Seed.Name
		})
	}
	// Build batches one bucket at a time.
	var batches [][]detailJob
	for _, k := range keys {
		batches = append(batches, partitionByAffinity(buckets[k])...)
	}
	return batches
}

// partitionByAffinity greedily groups jobs into batches that share
// affinity. Algorithm:
//
//  1. Start with one batch containing the first job (jobs are sorted
//     by name so this is deterministic).
//  2. For each remaining job, find the existing batch with the highest
//     AVERAGE affinity to that job, capped at detailBatchHardCap.
//  3. If no batch has positive affinity OR every batch is full, start
//     a new batch.
//  4. Adding to a batch beyond detailBatchSoftTarget incurs a -1
//     penalty so we prefer fresh batches when ties are close.
//
// Returns nil for an empty input, [[singleton]] for a 1-job input,
// and a list of batches for larger inputs.
func partitionByAffinity(jobs []detailJob) [][]detailJob {
	if len(jobs) == 0 {
		return nil
	}
	if len(jobs) == 1 {
		return [][]detailJob{{jobs[0]}}
	}
	batches := [][]detailJob{{jobs[0]}}
	for _, j := range jobs[1:] {
		bestBatch := -1
		bestScore := -1
		for bi := range batches {
			if len(batches[bi]) >= detailBatchHardCap {
				continue
			}
			score := batchAffinity(j, batches[bi])
			if len(batches[bi]) >= detailBatchSoftTarget {
				score-- // gentle penalty for going past the soft target
			}
			if score > bestScore {
				bestScore = score
				bestBatch = bi
			}
		}
		// If no batch has affinity OR all batches are full, start a new one.
		if bestBatch < 0 || bestScore <= 0 {
			batches = append(batches, []detailJob{j})
			continue
		}
		batches[bestBatch] = append(batches[bestBatch], j)
	}
	return batches
}

// batchAffinity is the average pairwise affinity of `j` against
// every existing member of `batch`. Average rather than max so a
// large batch dominated by unrelated entities doesn't keep
// attracting more on the strength of one match.
func batchAffinity(j detailJob, batch []detailJob) int {
	if len(batch) == 0 {
		return 0
	}
	total := 0
	for _, m := range batch {
		total += affinityScore(j, m)
	}
	return total / len(batch)
}

// affinityScore measures how related two detail jobs are. The
// scale is integers in [0, 7]. We deliberately keep this simple —
// every signal is cheap to compute and easy to explain when a
// batch looks wrong:
//
//	+3  same source file (jobs that point at the same file ARE
//	    conceptually related — they're operations on the same
//	    resource)
//	+2  same source directory (controllers/, repository/, etc.)
//	+1  share a deeper-than-3 directory prefix (e.g. both in
//	    src/main/java/.../campaign/)
//	+2  name prefix of >= 4 characters matches (catches
//	    `AccountRepository.X` vs `AccountRepository.Y`)
//	+2  share >= 2 name tokens (after camel-case + non-alnum split)
//	+1  share exactly 1 name token
//
// Zero score means "unrelated by any signal we trust". Such pairs
// land in separate batches.
func affinityScore(a, b detailJob) int {
	score := 0
	fa, fb := primaryFile(a.Seed), primaryFile(b.Seed)
	switch {
	case fa != "" && fa == fb:
		score += 3
	case fa != "" && fb != "" && filepath.Dir(fa) == filepath.Dir(fb):
		score += 2
	case fa != "" && fb != "":
		// Deeper shared prefix (4+ path segments) gets a small bump.
		ap := strings.Split(filepath.Dir(fa), "/")
		bp := strings.Split(filepath.Dir(fb), "/")
		shared := 0
		for i := 0; i < len(ap) && i < len(bp); i++ {
			if ap[i] != bp[i] {
				break
			}
			shared++
		}
		if shared >= 4 {
			score++
		}
	}
	na, nb := a.Seed.Name, b.Seed.Name
	if commonPrefixLen(na, nb) >= 4 {
		score += 2
	}
	overlap := tokenOverlap(na, nb)
	switch {
	case overlap >= 2:
		score += 2
	case overlap == 1:
		score++
	}
	return score
}

// primaryFile returns the first source_location.file of a seed
// (the file the model is most likely to open first), or "" if
// none exists.
func primaryFile(seed llmEntity) string {
	if len(seed.Locations) == 0 {
		return ""
	}
	return seed.Locations[0].File
}

func commonPrefixLen(a, b string) int {
	n := 0
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			break
		}
		n++
	}
	return n
}

// tokenOverlap returns the number of name-tokens shared between
// `a` and `b`. Names are split on non-alphanumeric AND camel-case
// boundaries; tokens shorter than 3 characters are discarded
// because 1-2 character tokens (`s`, `id`, `to`) are too generic
// to be a useful affinity signal.
func tokenOverlap(a, b string) int {
	ta := nameTokens(a)
	if len(ta) == 0 {
		return 0
	}
	tb := nameTokens(b)
	if len(tb) == 0 {
		return 0
	}
	n := 0
	for t := range ta {
		if _, ok := tb[t]; ok {
			n++
		}
	}
	return n
}

// nameTokens splits a name into lowercase tokens. "AccountController.findByEmail"
// → {"account", "controller", "find", "email"}. Camel-case is honoured;
// short tokens (< 3 chars) dropped to keep noise out.
func nameTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	var current strings.Builder
	flush := func() {
		t := current.String()
		current.Reset()
		if len(t) < 3 {
			return
		}
		out[strings.ToLower(t)] = struct{}{}
	}
	prevLower := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && prevLower {
				flush()
			}
			current.WriteRune(r)
			prevLower = unicode.IsLower(r)
		default:
			flush()
			prevLower = false
		}
	}
	flush()
	return out
}
