package scip

import (
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"
)

// buildChainIndex constructs an index where each entry in `chain` is a
// method whose body contains a single call to the next entry. The
// final entry is a leaf with no body. Used to test transitive walking.
//
// Chain example: ["Ctrl#h()", "Service#s()", "Repo#r()"] produces:
//   Ctrl#h() -> Service#s() -> Repo#r()
func buildChainIndex(t *testing.T, chain []string) *Index {
	t.Helper()
	docs := []*pb.Document{}
	for i, sym := range chain {
		if i == len(chain)-1 {
			// Leaf: just a definition occurrence, no body calls.
			docs = append(docs, &pb.Document{
				RelativePath: "f" + symFilename(sym) + ".java",
				Occurrences: []*pb.Occurrence{
					{
						Range:          []int32{0, 0, 0, 4},
						EnclosingRange: []int32{0, 0, 10, 0},
						Symbol:         sym,
						SymbolRoles:    int32(pb.SymbolRole_Definition),
					},
				},
				Symbols: []*pb.SymbolInformation{
					{Symbol: sym, Kind: pb.SymbolInformation_Method},
				},
			})
			continue
		}
		next := chain[i+1]
		docs = append(docs, docWithCalls("f"+symFilename(sym)+".java", sym, []string{next}))
	}
	return mustLoad(t, &pb.Index{
		Metadata:  &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: docs,
	})
}

// symFilename derives a stable filename suffix from a symbol — used
// only by the test helpers above to keep document paths distinct.
func symFilename(sym string) string {
	out := []byte{}
	for i := 0; i < len(sym); i++ {
		c := sym[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	return string(out)
}

// --- Walker ---

func TestWalkerSingleHop(t *testing.T) {
	idx := buildChainIndex(t, []string{
		"scip-java x ex/Ctrl#h().",
		"scip-java x ex/Repo#r().",
	})
	w := NewWalker(idx)
	target := "scip-java x ex/Repo#r()."
	paths := w.Walk("scip-java x ex/Ctrl#h().", WalkConfig{
		IsTarget: func(s string) bool { return s == target },
	})
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	p := paths[0]
	if p.EntrySymbol != "scip-java x ex/Ctrl#h()." {
		t.Errorf("EntrySymbol = %q", p.EntrySymbol)
	}
	if p.TargetSymbol != target {
		t.Errorf("TargetSymbol = %q", p.TargetSymbol)
	}
	if len(p.Steps) != 1 {
		t.Errorf("expected 1 step, got %d", len(p.Steps))
	}
}

func TestWalkerTransitive(t *testing.T) {
	idx := buildChainIndex(t, []string{
		"scip-java x ex/Ctrl#h().",
		"scip-java x ex/Svc#s().",
		"scip-java x ex/Repo#r().",
	})
	w := NewWalker(idx)
	target := "scip-java x ex/Repo#r()."
	paths := w.Walk("scip-java x ex/Ctrl#h().", WalkConfig{
		IsTarget: func(s string) bool { return s == target },
	})
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	if got := len(paths[0].Steps); got != 2 {
		t.Errorf("expected 2 steps (Ctrl→Svc→Repo), got %d", got)
	}
	if paths[0].Steps[0].CalleeSymbol != "scip-java x ex/Svc#s()." {
		t.Errorf("step[0] callee = %q", paths[0].Steps[0].CalleeSymbol)
	}
	if paths[0].Steps[1].CalleeSymbol != target {
		t.Errorf("step[1] callee = %q", paths[0].Steps[1].CalleeSymbol)
	}
}

func TestWalkerStopsAtMaxDepth(t *testing.T) {
	idx := buildChainIndex(t, []string{
		"scip-java x ex/L0#h().",
		"scip-java x ex/L1#h().",
		"scip-java x ex/L2#h().",
		"scip-java x ex/L3#h().",
		"scip-java x ex/Target#t().",
	})
	w := NewWalker(idx)
	paths := w.Walk("scip-java x ex/L0#h().", WalkConfig{
		MaxDepth: 2,
		IsTarget: func(s string) bool { return s == "scip-java x ex/Target#t()." },
	})
	// MaxDepth=2 means we walk L0→L1→L2 then stop, never reaching Target.
	if len(paths) != 0 {
		t.Errorf("expected 0 paths with MaxDepth=2, got %d", len(paths))
	}
}

func TestWalkerFollowsImplementations(t *testing.T) {
	// Controller calls IFoo#m() (interface). FooImpl#m() implements
	// IFoo#m() and calls Repo#r(). The walker MUST traverse the impl
	// boundary, otherwise the Spring DI pattern breaks.
	docs := []*pb.Document{
		// Controller body calls IFoo#m().
		docWithCalls("ctrl.java", "scip-java x ex/Ctrl#h().",
			[]string{"scip-java x ex/IFoo#m()."}),
		// IFoo declares m() (abstract; no body calls of its own).
		{
			RelativePath: "iface.java",
			Occurrences: []*pb.Occurrence{
				{
					Range:       []int32{0, 0, 0, 4},
					Symbol:      "scip-java x ex/IFoo#m().",
					SymbolRoles: int32(pb.SymbolRole_Definition),
				},
			},
			Symbols: []*pb.SymbolInformation{
				{Symbol: "scip-java x ex/IFoo#m().", Kind: pb.SymbolInformation_AbstractMethod},
			},
		},
		// FooImpl#m() body calls Repo#r() and declares an impl relationship to IFoo#m().
		{
			RelativePath: "impl.java",
			Occurrences: []*pb.Occurrence{
				{
					Range:          []int32{0, 0, 0, 4},
					EnclosingRange: []int32{0, 0, 5, 0},
					Symbol:         "scip-java x ex/FooImpl#m().",
					SymbolRoles:    int32(pb.SymbolRole_Definition),
				},
				{
					Range:          []int32{1, 4, 1, 14},
					EnclosingRange: []int32{1, 0, 1, 30},
					Symbol:         "scip-java x ex/Repo#r().",
					SymbolRoles:    int32(pb.SymbolRole_ReadAccess),
				},
			},
			Symbols: []*pb.SymbolInformation{
				{
					Symbol: "scip-java x ex/FooImpl#m().",
					Kind:   pb.SymbolInformation_Method,
					Relationships: []*pb.Relationship{
						{Symbol: "scip-java x ex/IFoo#m().", IsImplementation: true},
					},
				},
			},
		},
		// Repo#r() leaf.
		{
			RelativePath: "repo.java",
			Occurrences: []*pb.Occurrence{
				{
					Range:       []int32{0, 0, 0, 4},
					Symbol:      "scip-java x ex/Repo#r().",
					SymbolRoles: int32(pb.SymbolRole_Definition),
				},
			},
			Symbols: []*pb.SymbolInformation{{Symbol: "scip-java x ex/Repo#r()."}},
		},
	}
	idx := mustLoad(t, &pb.Index{
		Metadata:  &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: docs,
	})
	w := NewWalker(idx)
	paths := w.Walk("scip-java x ex/Ctrl#h().", WalkConfig{
		IsTarget: func(s string) bool { return s == "scip-java x ex/Repo#r()." },
	})
	if len(paths) == 0 {
		t.Fatalf("expected at least 1 path through interface impl, got 0")
	}
	// The first step is Ctrl→IFoo, the second is FooImpl→Repo. The
	// path may be 2 or 3 steps depending on how we model the impl
	// boundary; the contract is just "reaches Repo at all".
	last := paths[0].Steps[len(paths[0].Steps)-1]
	if last.CalleeSymbol != "scip-java x ex/Repo#r()." {
		t.Errorf("last step callee = %q", last.CalleeSymbol)
	}
}

func TestWalkerHandlesCycle(t *testing.T) {
	// A→B→A→B cycle: walker must terminate (cycle detection).
	docs := []*pb.Document{
		docWithCalls("a.java", "scip-java x ex/A#a().", []string{"scip-java x ex/B#b()."}),
		docWithCalls("b.java", "scip-java x ex/B#b().", []string{"scip-java x ex/A#a()."}),
	}
	idx := mustLoad(t, &pb.Index{
		Metadata:  &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: docs,
	})
	w := NewWalker(idx)
	// IsTarget that never matches → walker should still terminate.
	paths := w.Walk("scip-java x ex/A#a().", WalkConfig{
		IsTarget: func(s string) bool { return false },
	})
	if len(paths) != 0 {
		t.Errorf("no target → expected 0 paths, got %d", len(paths))
	}
}

func TestWalkerMaxPathsPerTarget(t *testing.T) {
	// A method that calls 5 different paths, all leading to the same
	// target. With MaxPathsPerTarget=2, we should record exactly 2.
	docs := []*pb.Document{
		docWithCalls("root.java", "scip-java x ex/Root#r().", []string{
			"scip-java x ex/A#a().",
			"scip-java x ex/B#b().",
			"scip-java x ex/C#c().",
			"scip-java x ex/D#d().",
			"scip-java x ex/E#e().",
		}),
		docWithCalls("a.java", "scip-java x ex/A#a().", []string{"scip-java x ex/T#t()."}),
		docWithCalls("b.java", "scip-java x ex/B#b().", []string{"scip-java x ex/T#t()."}),
		docWithCalls("c.java", "scip-java x ex/C#c().", []string{"scip-java x ex/T#t()."}),
		docWithCalls("d.java", "scip-java x ex/D#d().", []string{"scip-java x ex/T#t()."}),
		docWithCalls("e.java", "scip-java x ex/E#e().", []string{"scip-java x ex/T#t()."}),
		{
			RelativePath: "t.java",
			Occurrences: []*pb.Occurrence{
				{Range: []int32{0, 0, 0, 4}, Symbol: "scip-java x ex/T#t().", SymbolRoles: int32(pb.SymbolRole_Definition)},
			},
			Symbols: []*pb.SymbolInformation{{Symbol: "scip-java x ex/T#t()."}},
		},
	}
	idx := mustLoad(t, &pb.Index{
		Metadata:  &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: docs,
	})
	w := NewWalker(idx)
	paths := w.Walk("scip-java x ex/Root#r().", WalkConfig{
		MaxPathsPerTarget: 2,
		IsTarget:          func(s string) bool { return s == "scip-java x ex/T#t()." },
	})
	if len(paths) != 2 {
		t.Errorf("expected 2 paths (cap), got %d", len(paths))
	}
}

func TestWalkerDoesNotEmitZeroLengthPathWhenEntryIsTarget(t *testing.T) {
	idx := buildChainIndex(t, []string{"scip-java x ex/Solo#s()."})
	w := NewWalker(idx)
	paths := w.Walk("scip-java x ex/Solo#s().", WalkConfig{
		IsTarget: func(s string) bool { return true }, // entry == target
	})
	if len(paths) != 0 {
		t.Errorf("entry==target should not emit a zero-length path, got %d paths", len(paths))
	}
}

func TestSortPathsDeterministic(t *testing.T) {
	in := []Path{
		{TargetSymbol: "zeta", Steps: []CallSite{{At: Location{File: "b.java", StartLine: 5}}}},
		{TargetSymbol: "alpha", Steps: []CallSite{{At: Location{File: "a.java", StartLine: 1}}}},
		{TargetSymbol: "alpha", Steps: []CallSite{{At: Location{File: "a.java", StartLine: 3}}}},
	}
	SortPaths(in)
	if in[0].TargetSymbol != "alpha" || in[0].Steps[0].At.StartLine != 1 {
		t.Errorf("sort[0] = %+v", in[0])
	}
	if in[1].TargetSymbol != "alpha" || in[1].Steps[0].At.StartLine != 3 {
		t.Errorf("sort[1] = %+v", in[1])
	}
	if in[2].TargetSymbol != "zeta" {
		t.Errorf("sort[2] = %+v", in[2])
	}
}
