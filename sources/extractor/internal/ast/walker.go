package ast

import "context"

// PathStep is one hop in a call path from an exposure entry symbol to a
// dependency target.
type PathStep struct {
	// Order is the 1-based hop number.
	Order int
	// Caller is the qualified symbol of the enclosing function/method.
	Caller string
	// Callee is the resolved qualified symbol being called.
	Callee string
	// CalleeRaw is the identifier as written in source.
	CalleeRaw string
	// File is the file where the call occurs.
	File string
	// Range is the source position.
	Range Range
	// Arguments is the verbatim argument list.
	Arguments []ArgumentExpr
	// Condition is the control-flow context of this specific hop.
	Condition ConnectionCondition
	// Repetition describes whether this hop is inside a loop.
	Repetition Repetition
}

// CallPath is one resolved path from an entry symbol to a target dependency.
type CallPath struct {
	EntrySymbol  string
	TargetSymbol string
	Steps        []PathStep
}

// WalkConfig parameterises a walk.
type WalkConfig struct {
	// IsTarget returns true when a symbol is a dependency target.
	IsTarget func(symbol string) bool
	// Context lets callers abort long walks.
	Context context.Context
	// MaxDepth caps the hop count. Default 15.
	MaxDepth int
	// MaxPathsPerTarget caps how many paths to record per target. Default 8.
	MaxPathsPerTarget int
	// MaxPathsTotal caps total paths across all targets. Default 2000.
	MaxPathsTotal int
	// MaxVisitedEdges caps total edge expansions (anti-runaway). Default 100000.
	MaxVisitedEdges int
}

func (c WalkConfig) withDefaults() WalkConfig {
	if c.MaxDepth <= 0 {
		c.MaxDepth = 15
	}
	if c.MaxPathsPerTarget <= 0 {
		c.MaxPathsPerTarget = 8
	}
	if c.MaxPathsTotal <= 0 {
		c.MaxPathsTotal = 2000
	}
	if c.MaxVisitedEdges <= 0 {
		c.MaxVisitedEdges = 100000
	}
	if c.Context == nil {
		c.Context = context.Background()
	}
	return c
}

// Walker performs BFS over a ProjectIndex call graph to find paths from
// entry symbols to dependency targets.
type Walker struct {
	idx *ProjectIndex
}

// NewWalker constructs a Walker bound to the given index.
func NewWalker(idx *ProjectIndex) *Walker {
	return &Walker{idx: idx}
}

// Walk performs a BFS from entrySymbol, collecting paths that reach any
// symbol for which cfg.IsTarget returns true.
//
// The algorithm:
//  1. BFS ensures shortest paths are found first.
//  2. We only expand symbols that have a local definition (project code).
//     External/library symbols can be targets but are never expanded —
//     this prevents the graph from blowing up into library internals.
//  3. Each path node tracks the full set of visited symbols to prevent
//     cycles. The visited set is per-path (not global) so diamond patterns
//     (A→B→D and A→C→D) are both found.
//  4. Per-hop conditions and repetitions are derived from the EnclosingPath
//     of each CallSite, which tree-sitter populated at parse time.
func (w *Walker) Walk(entrySymbol string, cfg WalkConfig) []CallPath {
	paths, _ := w.WalkVerbose(entrySymbol, cfg)
	return paths
}

// WalkVerbose is Walk but also reports whether the walk was TRUNCATED by a cap
// (MaxPathsTotal, MaxVisitedEdges, or a MaxDepth cut on a node that still had
// outgoing calls). Truncation means "more paths may exist than were returned",
// so callers can distinguish "no connection" from "capped" and surface it
// instead of silently under-reporting (A2). Hitting MaxPathsPerTarget is NOT
// truncation — the dependency is still connected via an earlier path.
func (w *Walker) WalkVerbose(entrySymbol string, cfg WalkConfig) (paths []CallPath, truncated bool) {
	if w == nil || w.idx == nil || entrySymbol == "" || cfg.IsTarget == nil {
		return nil, false
	}
	cfg = cfg.withDefaults()

	type queueNode struct {
		symbol  string
		steps   []PathStep
		visited map[string]struct{}
		depth   int
	}

	queue := []queueNode{{
		symbol:  entrySymbol,
		visited: map[string]struct{}{entrySymbol: {}},
	}}

	hitCounts := map[string]int{}
	edgesVisited := 0

	for len(queue) > 0 {
		if cfg.Context.Err() != nil {
			break
		}
		if len(paths) >= cfg.MaxPathsTotal {
			truncated = true
			break
		}
		n := queue[0]
		queue = queue[1:]

		if n.depth >= cfg.MaxDepth {
			if len(w.idx.CallGraph[n.symbol]) > 0 {
				truncated = true
			}
			continue
		}

		calls := w.idx.CallGraph[n.symbol]
		for _, call := range calls {
			if cfg.Context.Err() != nil {
				break
			}
			if len(paths) >= cfg.MaxPathsTotal {
				truncated = true
				break
			}
			edgesVisited++
			if edgesVisited > cfg.MaxVisitedEdges {
				truncated = true
				return
			}

			// Derive step-level condition and repetition from the call's
			// enclosing control-flow context (populated by tree-sitter).
			cond, rep := DeriveConditionAndRepetition(call.EnclosingPath)

			for _, callee := range call.CalleeResolved {
				if _, inPath := n.visited[callee]; inPath {
					continue
				}

				step := PathStep{
					Order:      len(n.steps) + 1,
					Caller:     n.symbol,
					Callee:     callee,
					CalleeRaw:  call.CalleeRaw,
					File:       call.File,
					Range:      call.Range,
					Arguments:  call.Arguments,
					Condition:  cond,
					Repetition: rep,
				}
				newSteps := appendStep(n.steps, step)

				// If this callee is a target, record the path.
				if cfg.IsTarget(callee) {
					if hitCounts[callee] < cfg.MaxPathsPerTarget && len(paths) < cfg.MaxPathsTotal {
						paths = append(paths, CallPath{
							EntrySymbol:  entrySymbol,
							TargetSymbol: callee,
							Steps:        newSteps,
						})
						hitCounts[callee]++
					}
					// Don't expand target symbols — they are leaves.
					continue
				}

				// Only expand symbols defined in the project.
				if !w.idx.HasLocalDefinition(callee) {
					continue
				}

				// Only enqueue if we haven't exceeded the per-symbol path cap.
				newVisited := cloneVisited(n.visited, callee)
				queue = append(queue, queueNode{
					symbol:  callee,
					steps:   newSteps,
					visited: newVisited,
					depth:   n.depth + 1,
				})
			}
		}
	}

	return
}

// HasLocalDefinition reports whether a symbol is defined in the project (not
// just referenced from an external library).
func (idx *ProjectIndex) HasLocalDefinition(symbol string) bool {
	if idx == nil {
		return false
	}
	_, ok := idx.Symbols[symbol]
	return ok
}

func appendStep(existing []PathStep, s PathStep) []PathStep {
	out := make([]PathStep, len(existing)+1)
	copy(out, existing)
	out[len(existing)] = s
	return out
}

func cloneVisited(in map[string]struct{}, add string) map[string]struct{} {
	out := make(map[string]struct{}, len(in)+1)
	for k := range in {
		out[k] = struct{}{}
	}
	out[add] = struct{}{}
	return out
}
