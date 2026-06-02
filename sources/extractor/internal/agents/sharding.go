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
// Returns nil when the candidate space is small (caller falls back to a single
// whole-repo call) or when there is no AST index to derive a file tree from
// (single-call is then the safe, unchanged behaviour).
//
// The unit of sharding is the FILE. Each in-scope file is weighted by the
// number of this objective's AST candidates it contains (or 1 when the AST
// found none — so a big repo still shards to search for things the parser
// missed). Files are sorted by path (keeping same-directory files adjacent)
// and greedily packed into shards of bounded weight. Because the partition is
// over exact files, a single heavily-populated directory is split across
// shards rather than collapsing into one oversized call.
func planDiscoveryShards(idx *astpkg.ProjectIndex, obj objectives.Objective, subDir string) []discoveryShard {
	if idx == nil || len(idx.Files) == 0 {
		return nil
	}

	hints := buildObjectiveHints(idx, obj, subDir, nil)
	candidateCount := len(hints.Symbols) + len(hints.Bindings)
	useCandidates := candidateCount > 0

	// Per-file candidate weights.
	fileCandidates := map[string]int{}
	for _, s := range hints.Symbols {
		fileCandidates[s.File]++
	}
	for _, b := range hints.Bindings {
		fileCandidates[b.File]++
	}

	// In-scope files, sorted for deterministic, directory-adjacent packing.
	files := make([]string, 0, len(idx.Files))
	for f := range idx.Files {
		if fileInSubDir(f, subDir) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil
	}
	sort.Strings(files)

	weightOf := func(f string) int {
		if useCandidates {
			return fileCandidates[f] // 0 for files with no candidates
		}
		return 1
	}

	// Total weight gate: below the soft target, one whole-repo call.
	total := 0
	for _, f := range files {
		total += weightOf(f)
	}
	if total <= discoveryShardSoftTarget {
		return nil
	}

	// Greedily pack files into shards, AIMING for the soft target so the work
	// actually splits (a total between soft and hard would otherwise fit in a
	// single shard and defeat the purpose). The hard cap is the absolute
	// ceiling. A file heavier than the soft target on its own still starts
	// (and immediately closes) a shard — we never split below file
	// granularity. Files with zero candidates add no weight but are still
	// assigned, so dynamic/reflection code is searched.
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
		overWeight := w > 0 && curWeight+w > discoveryShardSoftTarget
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

// mergeShardEntities concatenates per-shard results and collapses exact
// shard-boundary duplicates (a directory can appear in one shard, but a symbol
// referenced from two files in different shards could be reported twice). The
// dedup key is type|name|firstLoc(file:line); on collision we keep the
// higher-confidence item and union its locations/evidence.
//
// This is a cheap pre-convert merge only; the authoritative semantic dedup is
// the later Stage-5 reconcile pass (which needs post-convert model.* types).
func mergeShardEntities(in [][]llmEntity) []llmEntity {
	var out []llmEntity
	index := map[string]int{} // key -> position in out
	for _, batch := range in {
		for _, e := range batch {
			k := shardEntityKey(e)
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

func shardEntityKey(e llmEntity) string {
	file, line := "", 0
	if len(e.Locations) > 0 {
		file = e.Locations[0].File
		line = e.Locations[0].StartLine
	}
	return strings.ToLower(e.Type) + "|" + strings.ToLower(e.Name) + "|" + file + ":" + itoa(line)
}

func unionLocations(a, b []llmLocation) []llmLocation {
	seen := map[string]struct{}{}
	var out []llmLocation
	add := func(locs []llmLocation) {
		for _, l := range locs {
			k := l.File + ":" + itoa(l.StartLine) + ":" + itoa(l.EndLine)
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
