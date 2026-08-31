package scip

import (
	"bytes"
	"testing"

	pb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// helper: build an in-memory index and load it through the same code path
// production uses. Keeps tests honest about the streaming loader.
func mustLoad(t *testing.T, idx *pb.Index) *Index {
	t.Helper()
	buf, err := proto.Marshal(idx)
	if err != nil {
		t.Fatalf("marshal Index: %v", err)
	}
	loaded, err := LoadFromReader("test", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

// docWithCalls builds a Document whose body of `defSym` contains
// occurrences for each callee in `calls`. The body's enclosing_range
// spans lines 0..10 so each call sits inside it.
func docWithCalls(path, defSym string, calls []string) *pb.Document {
	occs := []*pb.Occurrence{
		{
			Range:          []int32{0, 0, 0, 4}, // line 0, identifier
			EnclosingRange: []int32{0, 0, 10, 0}, // body line 0..10
			Symbol:         defSym,
			SymbolRoles:    int32(pb.SymbolRole_Definition),
		},
	}
	for i, c := range calls {
		occs = append(occs, &pb.Occurrence{
			Range:          []int32{int32(i + 1), 4, int32(i + 1), 14},
			EnclosingRange: []int32{int32(i + 1), 0, int32(i + 1), 30},
			Symbol:         c,
			SymbolRoles:    int32(pb.SymbolRole_ReadAccess),
		})
	}
	return &pb.Document{
		Language:     "java",
		RelativePath: path,
		Occurrences:  occs,
		Symbols: []*pb.SymbolInformation{
			{Symbol: defSym, DisplayName: lastSegment(defSym), Kind: pb.SymbolInformation_Method},
		},
	}
}

func lastSegment(sym string) string {
	for i := len(sym) - 1; i >= 0; i-- {
		switch sym[i] {
		case '/', '#', '.', ' ':
			return sym[i+1:]
		}
	}
	return sym
}

// --- SymbolAt ---

func TestSymbolAtBasic(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			docWithCalls("a.java", "scip-java x foo/Bar#m().", []string{"scip-java x foo/Repo#r()."}),
		},
	})
	// Method definition: range [0, 0, 0, 4].
	if got := idx.SymbolAt("a.java", 0, 2); got != "scip-java x foo/Bar#m()." {
		t.Errorf("SymbolAt def: got %q", got)
	}
	// Call inside body: range [1, 4, 1, 14].
	if got := idx.SymbolAt("a.java", 1, 7); got != "scip-java x foo/Repo#r()." {
		t.Errorf("SymbolAt call: got %q", got)
	}
	// Outside any range.
	if got := idx.SymbolAt("a.java", 100, 0); got != "" {
		t.Errorf("SymbolAt OOB: got %q, want \"\"", got)
	}
}

// --- CallsFrom ---

func TestCallsFromIncludesEveryCallInBody(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			docWithCalls("ctrl.java", "scip-java x ex/Controller#h().",
				[]string{
					"scip-java x ex/Service#s().",
					"scip-java x ex/Repo#r().",
				}),
		},
	})
	calls := idx.CallsFrom("scip-java x ex/Controller#h().")
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}
	want := []string{"scip-java x ex/Repo#r().", "scip-java x ex/Service#s()."}
	got := []string{calls[0].CalleeSymbol, calls[1].CalleeSymbol}
	// CallsFrom sorts by (file, line, col), so order is deterministic
	// — but the two calls are on different lines: Service on line 1,
	// Repo on line 2. Want: Service first then Repo.
	want = []string{"scip-java x ex/Service#s().", "scip-java x ex/Repo#r()."}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("call[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestCallsFromIgnoresNestedDefinitions(t *testing.T) {
	// A method body contains another Definition occurrence (nested
	// lambda). The walker must NOT recurse into it (would infinite-loop).
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			{
				Language:     "java",
				RelativePath: "a.java",
				Occurrences: []*pb.Occurrence{
					{
						Range:          []int32{0, 0, 0, 4},
						EnclosingRange: []int32{0, 0, 10, 0},
						Symbol:         "scip-java x ex/Outer#m().",
						SymbolRoles:    int32(pb.SymbolRole_Definition),
					},
					{
						Range:          []int32{2, 4, 2, 14},
						EnclosingRange: []int32{2, 0, 5, 0},
						Symbol:         "scip-java x ex/Lambda#$1().", // nested def
						SymbolRoles:    int32(pb.SymbolRole_Definition),
					},
					{
						Range:          []int32{6, 4, 6, 14},
						EnclosingRange: []int32{6, 0, 6, 30},
						Symbol:         "scip-java x ex/Other#o().",
						SymbolRoles:    int32(pb.SymbolRole_ReadAccess),
					},
				},
				Symbols: []*pb.SymbolInformation{
					{Symbol: "scip-java x ex/Outer#m()."},
				},
			},
		},
	})
	calls := idx.CallsFrom("scip-java x ex/Outer#m().")
	if len(calls) != 1 {
		t.Fatalf("expected 1 call (nested def skipped), got %d: %+v", len(calls), calls)
	}
	if calls[0].CalleeSymbol != "scip-java x ex/Other#o()." {
		t.Errorf("got %q", calls[0].CalleeSymbol)
	}
}

// --- Implementations ---

func TestImplementations(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			{
				RelativePath: "iface.java",
				Symbols: []*pb.SymbolInformation{
					{
						Symbol:      "scip-java x ex/Foo#m().",
						DisplayName: "m",
						Kind:        pb.SymbolInformation_AbstractMethod,
					},
				},
			},
			{
				RelativePath: "impl.java",
				Symbols: []*pb.SymbolInformation{
					{
						Symbol:      "scip-java x ex/FooImpl#m().",
						DisplayName: "m",
						Kind:        pb.SymbolInformation_Method,
						Relationships: []*pb.Relationship{
							{
								Symbol:           "scip-java x ex/Foo#m().",
								IsImplementation: true,
							},
						},
					},
				},
			},
		},
	})
	impls := idx.Implementations("scip-java x ex/Foo#m().")
	if len(impls) != 1 || impls[0] != "scip-java x ex/FooImpl#m()." {
		t.Errorf("Implementations = %v", impls)
	}
}

// --- DefinitionAt ---

func TestDefinitionAt(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			{
				RelativePath: "x.java",
				Occurrences: []*pb.Occurrence{
					{
						Range:       []int32{5, 4, 5, 10},
						Symbol:      "scip-java x ex/Foo#m().",
						SymbolRoles: int32(pb.SymbolRole_Definition),
					},
				},
			},
		},
	})
	locs := idx.DefinitionAt("scip-java x ex/Foo#m().")
	if len(locs) != 1 {
		t.Fatalf("expected 1 def, got %d", len(locs))
	}
	if locs[0].File != "x.java" || locs[0].StartLine != 5 || locs[0].StartCol != 4 {
		t.Errorf("def location = %+v", locs[0])
	}
}

// --- Resolver positional + name ---

func TestResolverPositional(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			{
				RelativePath: "ctrl.java",
				Occurrences: []*pb.Occurrence{
					{
						Range:       []int32{75, 4, 75, 24}, // line 75 zero-based = 76 1-based
						Symbol:      "scip-java x ex/Ctrl#delete().",
						SymbolRoles: int32(pb.SymbolRole_Definition),
					},
				},
			},
		},
	})
	r := NewResolver(idx)
	res := r.Resolve(EntityLocation{
		Name:      "delete",
		File:      "ctrl.java",
		StartLine: 76, // diffmind stores 1-based
		StartCol:  5,
	})
	if res.Source != "positional" {
		t.Errorf("source = %q, want positional", res.Source)
	}
	if len(res.Symbols) != 1 || res.Symbols[0] != "scip-java x ex/Ctrl#delete()." {
		t.Errorf("symbols = %v", res.Symbols)
	}
}

func TestResolverByQualified(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			{
				RelativePath: "repo.java",
				Symbols: []*pb.SymbolInformation{
					{
						Symbol:      "scip-java x ex/CampaignItemRepository#findById().",
						DisplayName: "findById",
					},
					{
						Symbol:      "scip-java x ex/Other#findById().",
						DisplayName: "findById",
					},
				},
			},
		},
	})
	r := NewResolver(idx)
	res := r.ResolveByQualified("CampaignItemRepository.findById")
	if res.Source != "exact_name" {
		t.Errorf("source = %q", res.Source)
	}
	if len(res.Symbols) != 1 {
		t.Fatalf("expected 1 match (qualified narrows by class), got %v", res.Symbols)
	}
	if res.Symbols[0] != "scip-java x ex/CampaignItemRepository#findById()." {
		t.Errorf("symbol = %q", res.Symbols[0])
	}
}

// --- PrefixMatchSymbols ---

func TestPrefixMatchSymbols(t *testing.T) {
	idx := mustLoad(t, &pb.Index{
		Metadata: &pb.Metadata{ProjectRoot: "file:///x", ToolInfo: &pb.ToolInfo{Name: "t"}},
		Documents: []*pb.Document{
			{
				RelativePath: "a.java",
				Occurrences: []*pb.Occurrence{
					{
						Range:       []int32{0, 0, 0, 1},
						Symbol:      "scip-java x ex/A#m().",
						SymbolRoles: int32(pb.SymbolRole_Definition),
					},
					{
						Range:       []int32{1, 0, 1, 1},
						Symbol:      "scip-java x ex/A#n().",
						SymbolRoles: int32(pb.SymbolRole_Definition),
					},
					{
						Range:       []int32{2, 0, 2, 1},
						Symbol:      "scip-java x ex/B#m().",
						SymbolRoles: int32(pb.SymbolRole_Definition),
					},
				},
			},
		},
	})
	got := idx.PrefixMatchSymbols("scip-java x ex/A#")
	if len(got) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(got), got)
	}
}
