package agents

import (
	"path"
	"sort"
	"strings"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
	"github.com/mohammad-safakhou/diffmind/internal/objectives"
)

// sharding.go implements orchestrator-driven decomposition of Stage-1
// discovery. When an objective's candidate space is large, instead of one
// whole-repo LLM call we split discovery into directory-scoped sub-tasks
// ("shards"), run each as a child job, and merge the results. This raises
// recall on heavy-CRUD services where a single "enumerate everything" call
// truncates or fatigues.
//
// Design notes:
//   - Sharding is keyed to the FILESYSTEM DIRECTORY TREE (from idx.Files),
//     NOT to AST symbols, so it works even when static analysis is weak: every
//     in-scope directory lands in exactly one shard, so dynamically-registered
//     / reflection-based entities the AST can't see are still searched within
//     their directory's shard.
//   - AST hints (when present) only REFINE a shard (its candidate context),
//     they never decide which directories exist.
//   - Below the soft target we return nil → the caller keeps today's single
//     whole-repo call, so small repos are completely unaffected.

// Shard sizing. Measured in AST candidates when the index has them for this
// objective, else in source files. Private consts (same deliberate-tradeoff
// convention as grouping.go): changing them is a real cost/quality decision.
const (
	discoveryShardSoftTarget = 40 // at/below this estimate: one call, no sharding
	discoveryShardHardCap    = 60 // max candidate weight packed into a single shard
	maxShardFiles            = 60 // hard ceiling on files listed in one shard's SCOPE
)

// discoveryShard is one scoped sub-task of an objective's discovery. Files is
// the exact set of source files the shard owns (a partition of the in-scope
// tree), so shards never overlap and a single fat directory can still be split
// across shards. Dirs is the deduped directory list, for the SCOPE line and
// event display.
type discoveryShard struct {
	Index int            // 0-based; used for jobID + checkpoint key
	Files []string       // exact source files this shard covers
	Dirs  []string       // distinct directories of Files (display/scope summary)
	Hints objectiveHints // AST hints restricted to this shard's files
}

// planDiscoveryShards decides whether and how to split obj's discovery.
// Returns nil — meaning the caller makes a single whole-repo call — unless the
// objective has STRONG static evidence (many AST candidates) concentrated
// enough to be worth fanning out.
//
// Evidence-gated, candidate-clustered design:
//
//   - We shard ONLY the files that actually contain this objective's
//     candidates (matching symbols / framework bindings). A repo with no
//     candidates for an objective (e.g. an RPC objective on a repo with no
//     gRPC) returns nil → ONE cheap whole-repo call. The previous behaviour
//     weighted every file equally when the AST found nothing, so a 200-file
//     repo fanned an empty objective into 4-7 whole-repo scans — pure cost,
//     zero recall. Sharding now scales with evidence, not raw file count.
//
//   - Candidate files are sorted by path (same-directory files adjacent) and
//     greedily packed by candidate weight, so a shard is a COHERENT CLUSTER
//     (e.g. repository/ + entity/ for db_operation) rather than an arbitrary
//     alphabetical slice spanning controllers, enums and config.
//
//   - Shards scope WHERE findings are reported, never what may be READ: the
//     prompt lets each shard open shared base classes / config / helpers
//     anywhere in the repo (see discoveryScopeBlock). Each candidate file
//     belongs to exactly one shard, so declarations are partitioned and
//     cross-shard double-reporting is avoided without blinding a shard to
//     shared code.
//
// Tradeoff: a dynamic/reflection entity whose declaration sits in a file with
// NO static candidates is not covered when an objective shards. That is the
// accepted cost of evidence-gating — it only applies to high-evidence
// objectives (whose reflection tail is small), and low-evidence objectives
// still get a single whole-repo call that searches everything.
func planDiscoveryShards(idx *astpkg.ProjectIndex, obj objectives.Objective, subDir string) []discoveryShard {
	if idx == nil || len(idx.Files) == 0 {
		return nil
	}

	// Per-file candidate weights (uncapped). Files absent from the map have no
	// candidates and are never sharded.
	fileCandidates := objectiveCandidateWeights(idx, obj, subDir)
	if len(fileCandidates) == 0 {
		return nil // no evidence → single whole-repo call
	}

	// Candidate-bearing files only, sorted for deterministic, directory-
	// adjacent packing.
	files := make([]string, 0, len(fileCandidates))
	total := 0
	for f, w := range fileCandidates {
		files = append(files, f)
		total += w
	}
	sort.Strings(files)

	// Total candidate weight gate: below the soft target, one whole-repo call.
	if total <= discoveryShardSoftTarget {
		return nil
	}

	weightOf := func(f string) int {
		if w := fileCandidates[f]; w > 0 {
			return w
		}
		return 1
	}

	// Greedily pack candidate files into shards, AIMING for the soft target so
	// the work actually splits (a total between soft and hard would otherwise
	// fit in a single shard and defeat the purpose). The hard cap is the
	// absolute ceiling. A file heavier than the soft target on its own still
	// starts (and immediately closes) a shard — we never split below file
	// granularity.
	var shards []discoveryShard
	var cur []string
	curWeight := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		shards = append(shards, discoveryShard{Files: append([]string(nil), cur...)})
		cur = nil
		curWeight = 0
	}
	for _, f := range files {
		w := weightOf(f)
		overWeight := curWeight+w > discoveryShardSoftTarget
		overFiles := len(cur) >= maxShardFiles
		if curWeight > 0 && (overWeight || overFiles) {
			flush()
		}
		cur = append(cur, f)
		curWeight += w
	}
	flush()

	if len(shards) <= 1 {
		return nil // nothing gained — single call
	}

	for i := range shards {
		shards[i].Index = i
		shards[i].Dirs = distinctDirs(shards[i].Files)
		// Hint scope = exact files (an exact path is a prefix of itself), so a
		// shard's hints cover only its own files even when a directory is split.
		shards[i].Hints = buildObjectiveHints(idx, obj, subDir, shards[i].Files)
	}
	return shards
}

func fileInSubDir(file, subDir string) bool {
	if subDir == "" {
		return true
	}
	return strings.HasPrefix(file, strings.TrimSuffix(subDir, "/")+"/")
}

// distinctDirs returns the sorted, deduped set of directories of the files.
func distinctDirs(files []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, f := range files {
		d := path.Dir(f)
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// mergeShardEntities concatenates per-shard results and collapses
// shard-boundary duplicates (a symbol referenced from two files in different
// shards, or the same high-level dependency reported by adjacent shards). The
// dedup key is the objective's SEMANTIC key (e.g. method+path for routes,
// resource+operation for db ops), falling back to type|name|firstLoc for
// objectives without a semantic identity. On collision we keep the
// higher-confidence item and union its locations/evidence.
//
// This is a cheap pre-convert merge only; the authoritative semantic dedup is
// the later Stage-5 reconcile pass (which needs post-convert model.* types).
func mergeShardEntities(obj objectives.Objective, in [][]llmEntity) []llmEntity {
	var out []llmEntity
	index := map[string]int{} // key -> position in out
	for _, batch := range in {
		for _, e := range batch {
			k := discoverySemanticKey(obj, e)
			if pos, ok := index[k]; ok {
				if e.Confidence > out[pos].Confidence {
					// Keep the higher-confidence base, but union locations/evidence.
					merged := e
					merged.Locations = unionLocations(out[pos].Locations, e.Locations)
					merged.Evidence = append(append([]llmEvidence(nil), out[pos].Evidence...), e.Evidence...)
					out[pos] = merged
				} else {
					out[pos].Locations = unionLocations(out[pos].Locations, e.Locations)
					out[pos].Evidence = append(out[pos].Evidence, e.Evidence...)
				}
				continue
			}
			index[k] = len(out)
			out = append(out, e)
		}
	}
	return out
}

func unionLocations(a, b []llmLocation) []llmLocation {
	seen := map[string]struct{}{}
	var out []llmLocation
	add := func(locs []llmLocation) {
		for _, l := range locs {
			k := l.File + ":" + Itoa(l.StartLine) + ":" + Itoa(l.EndLine)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, l)
		}
	}
	add(a)
	add(b)
	return out
}
