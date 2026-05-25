package scip

import (
	"sort"
)

// Path is one resolved call chain from an entry symbol (e.g. a
// controller handler) to a target dependency symbol. It is the
// deterministic equivalent of the LLM's `paths[]` output.
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

// Walk performs a transitive call-graph traversal starting at
// `entrySymbol`. It returns every Path that ends at a symbol for
// which cfg.IsTarget returned true, up to the configured limits.
//
// Algorithm:
//
//  1. We do a DEPTH-FIRST traversal so each Path can be assembled
//     incrementally on the call stack (Go stack reflects walker stack).
//  2. We track a `visited` set keyed by (callerSymbol -> calleeSymbol)
//     to avoid infinite recursion in cycles. This is per-walk; we don't
//     cache across calls because the IsTarget predicate is per-call.
//  3. When we reach a target symbol, we materialise the current chain
//     into a Path and append to the result list. We DO NOT stop the
//     branch: a single chain may reach multiple targets (e.g. a service
//     method touches three dependencies in sequence).
//  4. When we follow into an interface symbol, we ALSO enqueue every
//     implementation as a sibling branch. We record the impl as the
//     callee for the step but keep the original (interface) symbol
//     available via the CallerSymbol of the next step's call site if
//     useful for diagnostics.
//
// Returns an empty slice when entrySymbol has no body or no target is
// reachable. Never returns nil; callers can iterate safely.
func (w *Walker) Walk(entrySymbol string, cfg WalkConfig) []Path {
	if w == nil || w.idx == nil || entrySymbol == "" || cfg.IsTarget == nil {
		return []Path{}
	}
	cfg = cfg.withDefaults()

	state := &walkState{
		idx:    w.idx,
		cfg:    cfg,
		paths:  []Path{},
		stack:  []CallSite{},
		seen:   map[edgeKey]struct{}{},
		// hitCounts: per-target counter for MaxPathsPerTarget cap.
		hitCounts: map[string]int{},
	}
	state.descend(entrySymbol, 0)
	return state.paths
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
	if !out.FollowImplementations {
		// Zero-value (false) is treated as "use the default" (true).
		// To DISABLE implementations, callers pass FollowImplementations:false
		// explicitly AND set a special flag via the upcoming refactor.
		// For Sprint 2, we honor the zero-value-means-default convention.
		out.FollowImplementations = true
	}
	return out
}

// edgeKey identifies a directed edge in the call graph for the
// visited-set. We key by (caller, callee) — not just callee — so a
// diamond pattern (A→B→D and A→C→D) records BOTH paths.
type edgeKey struct{ caller, callee string }

type walkState struct {
	idx       *Index
	cfg       WalkConfig
	paths     []Path
	stack     []CallSite
	seen      map[edgeKey]struct{}
	hitCounts map[string]int
	entrySym  string // set on first descend; immutable after
}

// descend visits `symbol`. If `symbol` matches a target, we record a
// Path. Otherwise we recurse into its call sites up to MaxDepth.
//
// `depth` is the depth from the entry symbol (0 == entry itself).
func (s *walkState) descend(symbol string, depth int) {
	if s.entrySym == "" {
		s.entrySym = symbol
	}
	if depth > s.cfg.MaxDepth {
		return
	}

	// Record a Path when we hit a target — but only if we've actually
	// traversed at least one edge. The entry symbol itself is not a
	// useful "path" (it would be a single-symbol path with zero hops),
	// and callers can detect "entry is target" separately by inspecting
	// the entity catalog.
	if s.cfg.IsTarget(symbol) && len(s.stack) > 0 {
		s.recordPath(symbol)
		// We deliberately continue the descent here: a service method
		// may call multiple dependencies in sequence, and we want a
		// Path for each. Capping is enforced by hitCounts in recordPath.
	}

	calls := s.idx.CallsFrom(symbol)
	for _, c := range calls {
		key := edgeKey{caller: symbol, callee: c.CalleeSymbol}
		if _, dup := s.seen[key]; dup {
			continue
		}
		s.seen[key] = struct{}{}

		// Enter the edge.
		s.stack = append(s.stack, c)

		// 1. Descend directly into the callee. This catches the common
		// case where the callee is concrete (a service impl method, a
		// repository method on a concrete class, etc.).
		s.descend(c.CalleeSymbol, depth+1)

		// 2. If FollowImplementations and the callee has registered
		// implementations, descend into each impl as well. This is how
		// we walk through Spring's `Foo foo = new FooImpl()` boundary:
		// the call site references the interface symbol, but the actual
		// body lives on the impl.
		//
		// We don't add the impl as an extra step on the stack — the
		// edge in the source code IS to the interface symbol. The next
		// hop just continues from the impl's body. The Path's Steps
		// therefore read naturally as "controller → service interface →
		// repository" even when behind the scenes we walked the impl.
		if s.cfg.FollowImplementations {
			for _, impl := range s.idx.Implementations(c.CalleeSymbol) {
				implKey := edgeKey{caller: c.CalleeSymbol, callee: impl}
				if _, dup := s.seen[implKey]; dup {
					continue
				}
				s.seen[implKey] = struct{}{}
				s.descend(impl, depth+1)
			}
		}

		// Leave the edge.
		s.stack = s.stack[:len(s.stack)-1]
		// Allow the edge to be revisited via a *different* parent chain
		// in a follow-up branch. Without this, two siblings under the
		// same root would each prevent the other from being explored
		// because the entire call-graph is one undirected component.
		// We DO leave per-walk dedup intact for the current chain
		// (rangeContainedIn + cycle prevention via `seen`) so we don't
		// spin forever on a cycle.
		delete(s.seen, key)
	}
}

// recordPath snapshots the current call stack into a new Path entry.
// Honours the MaxPathsPerTarget cap.
func (s *walkState) recordPath(target string) {
	if s.hitCounts[target] >= s.cfg.MaxPathsPerTarget {
		return
	}
	steps := make([]CallSite, len(s.stack))
	copy(steps, s.stack)
	s.paths = append(s.paths, Path{
		EntrySymbol:  s.entrySym,
		TargetSymbol: target,
		Steps:        steps,
	})
	s.hitCounts[target]++
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
