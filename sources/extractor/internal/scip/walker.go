package scip

import (
	"context"
	"sort"
)

// Path is one resolved call chain from an entry symbol (e.g. a
// controller handler) to a target dependency symbol. It is the
// deterministic call-path output.
//
// Steps is ordered from entry → target. The first step's CallerSymbol
// is the entry symbol; the last step's CalleeSymbol is the target.
// Intermediate steps' CallerSymbol equals the previous step's
// CalleeSymbol, so the chain reads as a sequence of edges.
type Path struct {
	// EntrySymbol is the symbol the walk started from. Echoed in
	// every Path so callers don't need to thread it separately.
	EntrySymbol string
	// TargetSymbol is the symbol the walk reached. May be different
	// from the actual interface-typed symbol the caller requested if
	// implementation resolution kicked in (we record the symbol we
	// physically traversed to).
	TargetSymbol string
	// Steps is the ordered chain of call sites. Length >= 1.
	Steps []CallSite
}

// WalkConfig parameterises a transitive walk. Sensible zero-values
// trigger the defaults documented per-field.
type WalkConfig struct {
	// MaxDepth caps how many hops deep the walker recurses. Zero
	// means "unlimited" — note that real Spring controller chains
	// rarely exceed 8 hops and most repos finish at 3-5. Setting
	// MaxDepth too low truncates legitimate chains; setting it too
	// high mostly costs memory because the search space is bounded
	// by the call graph's branching factor, not by depth alone.
	//
	// Default (when zero): 25.
	MaxDepth int

	// MaxPathsPerTarget bounds how many independent paths to record
	// per (entry, target) pair. A controller may invoke a
	// dependency through several validation branches; we want a
	// handful for the UI but not 200. Once this is hit, additional
	// branches that would lead to the same target are pruned.
	//
	// Default (when zero): 8.
	MaxPathsPerTarget int

	// MaxPathsTotal bounds the total number of paths recorded by one walk.
	// Real handlers should produce a small handful of useful paths; dense call
	// graphs can otherwise materialise thousands of variants.
	//
	// Default (when zero): 1000.
	MaxPathsTotal int

	// MaxPathsPerSymbol bounds how many distinct partial paths we keep to the
	// same intermediate local symbol. This is the key to avoiding exponential
	// blow-ups on diamond-shaped service graphs while still preserving a few
	// alternative conditional branches.
	//
	// Default (when zero): 3.
	MaxPathsPerSymbol int

	// MaxVisitedEdges bounds total edge visits for one walk. The walker is
	// deterministic but call graphs can be dense enough to explode
	// combinatorially when revisiting shared subgraphs through many parents.
	//
	// Default (when zero): 50000.
	MaxVisitedEdges int

	// Context lets callers abort long walks when the pipeline is cancelled or a
	// per-stage budget expires. Nil means context.Background().
	Context context.Context

	// FollowImplementations controls whether the walker, on hitting
	// an interface method symbol, descends into its implementations
	// (Spring DI pattern, TypeScript decorators, etc.). When false
	// the walk terminates at the interface boundary — useful when
	// the user wants only intra-source-file paths.
	//
	// Default (when zero): true.
	FollowImplementations bool

	// IsTarget is called once per visited symbol. The walker records
	// a Path whenever IsTarget returns true. Callers MUST set this:
	// the zero value (nil) is treated as "never" and the walk yields
	// no paths.
	IsTarget func(symbol string) bool
}

// Walker walks the call graph of an Index. It is stateless apart from
// its Index reference; create one per query.
type Walker struct {
	idx *Index
}

// NewWalker constructs a Walker bound to the given index.
func NewWalker(idx *Index) *Walker {
	return &Walker{idx: idx}
}

// Walk performs a targeted project-local call-graph traversal starting at
// `entrySymbol`. It returns the shortest/source-order paths that end at a
// symbol for which cfg.IsTarget returned true, up to the configured limits.
//
// Algorithm:
//
//  1. We do BREADTH-FIRST traversal, so the first paths found are the shortest
//     app-code paths from exposure to dependency.
//  2. We only expand symbols defined in the project index. External/library
//     symbols may be terminal targets, but they are never expanded.
//  3. We keep only a small number of partial paths per intermediate symbol.
//     This gives useful branch diversity without enumerating every diamond in
//     a service graph.
//  4. When we hit an interface symbol, we enqueue local implementations as
//     sibling branches while preserving the source callsite evidence.
//
// Returns an empty slice when entrySymbol has no body or no target is
// reachable. Never returns nil; callers can iterate safely.
func (w *Walker) Walk(entrySymbol string, cfg WalkConfig) []Path {
	if w == nil || w.idx == nil || entrySymbol == "" || cfg.IsTarget == nil {
		return []Path{}
	}
	cfg = cfg.withDefaults()
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}

	return (&walkSearch{idx: w.idx, cfg: cfg}).run(entrySymbol)
}

// withDefaults returns a copy of cfg with zero-valued fields replaced
// by sensible defaults. We mutate a copy so callers can keep their
// original struct unchanged.
func (c WalkConfig) withDefaults() WalkConfig {
	out := c
	if out.MaxDepth <= 0 {
		out.MaxDepth = 25
	}
	if out.MaxPathsPerTarget <= 0 {
		out.MaxPathsPerTarget = 8
	}
	if out.MaxPathsTotal <= 0 {
		out.MaxPathsTotal = 1000
	}
	if out.MaxPathsPerSymbol <= 0 {
		out.MaxPathsPerSymbol = 3
	}
	if out.MaxVisitedEdges <= 0 {
		out.MaxVisitedEdges = 50000
	}
	if !out.FollowImplementations {
		// Zero-value (false) is treated as "use the default" (true).
		// To DISABLE implementations, callers pass FollowImplementations:false
		// explicitly AND set a special flag via the upcoming refactor.
		// For Sprint 2, we honor the zero-value-means-default convention.
		out.FollowImplementations = true
	}
	return out
}

type walkSearch struct {
	idx       *Index
	cfg       WalkConfig
	paths     []Path
	hitCounts map[string]int
	kept      map[string]int
	visited   int
}

type walkNode struct {
	symbol string
	depth  int
	steps  []CallSite
	seen   map[string]struct{}
}

func (s *walkSearch) run(entrySymbol string) []Path {
	s.paths = []Path{}
	s.hitCounts = map[string]int{}
	s.kept = map[string]int{entrySymbol: 1}
	queue := []walkNode{{
		symbol: entrySymbol,
		seen:   map[string]struct{}{entrySymbol: {}},
	}}

	for len(queue) > 0 {
		if s.cfg.Context.Err() != nil || len(s.paths) >= s.cfg.MaxPathsTotal {
			break
		}
		n := queue[0]
		queue = queue[1:]
		if n.depth >= s.cfg.MaxDepth {
			continue
		}

		for _, call := range s.idx.CallsFrom(n.symbol) {
			if s.cfg.Context.Err() != nil || len(s.paths) >= s.cfg.MaxPathsTotal {
				return s.paths
			}
			s.visited++
			if s.visited > s.cfg.MaxVisitedEdges {
				return s.paths
			}

			steps := appendSteps(n.steps, call)
			callee := call.CalleeSymbol
			if s.cfg.IsTarget(callee) {
				s.recordPath(entrySymbol, callee, steps)
				// Dependencies are terminal for connection mapping. Do not walk
				// through repository/client/library bodies after recording the hit.
				continue
			}

			if s.cfg.FollowImplementations {
				for _, impl := range s.idx.Implementations(callee) {
					if _, inPath := n.seen[impl]; inPath {
						continue
					}
					if s.cfg.IsTarget(impl) {
						s.recordPath(entrySymbol, impl, steps)
						continue
					}
					if s.enqueueable(impl) {
						queue = append(queue, walkNode{
							symbol: impl,
							depth:  n.depth + 1,
							steps:  steps,
							seen:   cloneSeenWith(n.seen, impl),
						})
					}
				}
			}

			if _, inPath := n.seen[callee]; inPath {
				continue
			}
			if s.enqueueable(callee) {
				queue = append(queue, walkNode{
					symbol: callee,
					depth:  n.depth + 1,
					steps:  steps,
					seen:   cloneSeenWith(n.seen, callee),
				})
			}
		}
	}
	return s.paths
}

func (s *walkSearch) enqueueable(symbol string) bool {
	if !s.idx.HasLocalDefinition(symbol) {
		return false
	}
	if s.kept[symbol] >= s.cfg.MaxPathsPerSymbol {
		return false
	}
	s.kept[symbol]++
	return true
}

func (s *walkSearch) recordPath(entry, target string, steps []CallSite) {
	if len(steps) == 0 || len(s.paths) >= s.cfg.MaxPathsTotal {
		return
	}
	if s.hitCounts[target] >= s.cfg.MaxPathsPerTarget {
		return
	}
	s.paths = append(s.paths, Path{EntrySymbol: entry, TargetSymbol: target, Steps: appendSteps(nil, steps...)})
	s.hitCounts[target]++
}

func appendSteps(base []CallSite, steps ...CallSite) []CallSite {
	out := make([]CallSite, 0, len(base)+len(steps))
	out = append(out, base...)
	out = append(out, steps...)
	return out
}

func cloneSeenWith(in map[string]struct{}, symbol string) map[string]struct{} {
	out := make(map[string]struct{}, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[symbol] = struct{}{}
	return out
}

// SortPaths orders a slice of Paths for deterministic output.
// Primary key: TargetSymbol (so connections to the same dep cluster).
// Secondary key: Steps[0].At (file:line of the first hop), so paths
// originating from different positions in the same handler appear in
// source order.
//
// Used by callers (notably the connections stage) that need stable IDs.
func SortPaths(paths []Path) {
	sort.SliceStable(paths, func(i, j int) bool {
		if paths[i].TargetSymbol != paths[j].TargetSymbol {
			return paths[i].TargetSymbol < paths[j].TargetSymbol
		}
		a := paths[i].Steps
		b := paths[j].Steps
		if len(a) == 0 || len(b) == 0 {
			return len(a) < len(b)
		}
		if a[0].At.File != b[0].At.File {
			return a[0].At.File < b[0].At.File
		}
		if a[0].At.StartLine != b[0].At.StartLine {
			return a[0].At.StartLine < b[0].At.StartLine
		}
		return a[0].At.StartCol < b[0].At.StartCol
	})
}
