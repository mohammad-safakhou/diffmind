package agents

import (
	"strings"
	"testing"

	astpkg "github.com/mohammad-safakhou/diffmind/internal/ast"
)

// bigIndex builds a synthetic index with `perModule` http-route controllers in
// each of `modules` top-level directories.
func bigIndex(modules []string, perModule int) *astpkg.ProjectIndex {
	idx := &astpkg.ProjectIndex{
		Symbols: map[string][]astpkg.SymbolDef{},
		Files:   map[string]*astpkg.FileAST{},
	}
	for _, m := range modules {
		for i := 0; i < perModule; i++ {
			file := m + "/C" + itoa(i) + ".java"
			q := m + ".C" + itoa(i)
			idx.Symbols[q] = []astpkg.SymbolDef{sym(q, "C"+itoa(i), "FooController", file, uint32(i+1), "RestController", "GetMapping")}
			idx.Files[file] = &astpkg.FileAST{Path: file}
		}
	}
	return idx
}

func TestPlanShards_NilBelowSoftTarget(t *testing.T) {
	// 2 modules × 5 = 10 candidates, below soft target → single call (nil).
	idx := bigIndex([]string{"src/api/orders", "src/api/users"}, 5)
	if shards := planDiscoveryShards(idx, objByType(t, "http_route"), ""); shards != nil {
		t.Fatalf("expected nil (single call) below soft target, got %d shards", len(shards))
	}
}

func TestPlanShards_NilIndex(t *testing.T) {
	if shards := planDiscoveryShards(nil, objByType(t, "http_route"), ""); shards != nil {
		t.Fatalf("nil index must yield nil shards")
	}
}

func TestPlanShards_SplitsAboveTarget(t *testing.T) {
	// 6 dirs × 20 = 120 candidates → must shard, each shard's candidate weight ≤ hard cap.
	modules := []string{"src/api/a", "src/api/b", "src/api/c", "src/api/d", "src/api/e", "src/api/f"}
	idx := bigIndex(modules, 20)
	shards := planDiscoveryShards(idx, objByType(t, "http_route"), "")
	if len(shards) < 2 {
		t.Fatalf("expected multiple shards, got %d", len(shards))
	}
	// Every in-scope file appears in exactly one shard (a partition).
	seen := map[string]int{}
	for _, sh := range shards {
		w := 0
		for _, f := range sh.Files {
			seen[f]++
			w++
		}
		if w > discoveryShardHardCap {
			t.Fatalf("shard %d has %d files, exceeds hard cap %d", sh.Index, w, discoveryShardHardCap)
		}
		// Each shard's hints must be scoped to its own files.
		fileSet := map[string]bool{}
		for _, f := range sh.Files {
			fileSet[f] = true
		}
		for _, s := range sh.Hints.Symbols {
			if !fileSet[s.File] {
				t.Fatalf("shard %d hint %s outside its files", sh.Index, s.File)
			}
		}
	}
	for _, m := range modules {
		for i := 0; i < 20; i++ {
			f := m + "/C" + itoa(i) + ".java"
			if seen[f] != 1 {
				t.Fatalf("file %s appeared %d times across shards (want exactly 1)", f, seen[f])
			}
		}
	}
	for i, sh := range shards {
		if sh.Index != i {
			t.Fatalf("shard index = %d, want %d", sh.Index, i)
		}
	}
}

func TestPlanShards_SplitsSingleFatDirectory(t *testing.T) {
	// All candidates in ONE directory (the heavy-CRUD case) must still split.
	idx := bigIndex([]string{"src/api"}, 120)
	shards := planDiscoveryShards(idx, objByType(t, "http_route"), "")
	if len(shards) < 2 {
		t.Fatalf("a single fat directory of 120 candidates must split, got %d shards", len(shards))
	}
}

func TestPlanShards_NilWithoutASTCandidates(t *testing.T) {
	// Index has many FILES but the objective's matcher finds NO candidate
	// symbols (files carry no relevant annotations). Sharding is now
	// EVIDENCE-GATED: with zero candidates it must NOT fan out — the caller
	// makes a single cheap whole-repo call instead of N empty scans. This is
	// the fix for empty objectives (e.g. RPC on a non-gRPC repo) being
	// sharded 5-7 ways for zero recall.
	idx := &astpkg.ProjectIndex{Symbols: map[string][]astpkg.SymbolDef{}, Files: map[string]*astpkg.FileAST{}}
	modules := []string{"src/a", "src/b", "src/c", "src/d", "src/e"}
	for _, m := range modules {
		for i := 0; i < 12; i++ { // 5×12 = 60 files, no http annotations
			f := m + "/F" + itoa(i) + ".go"
			idx.Files[f] = &astpkg.FileAST{Path: f}
		}
	}
	if shards := planDiscoveryShards(idx, objByType(t, "http_route"), ""); shards != nil {
		t.Fatalf("expected nil (single call) when AST has no candidates, got %d shards", len(shards))
	}
}

// TestPlanShards_OnlyClustersCandidateFiles proves shards cover candidate-
// bearing files only — non-candidate files (controllers/enums/config for a
// db objective) are never given dedicated shards, so cost tracks evidence.
func TestPlanShards_OnlyClustersCandidateFiles(t *testing.T) {
	idx := &astpkg.ProjectIndex{Symbols: map[string][]astpkg.SymbolDef{}, Files: map[string]*astpkg.FileAST{}}
	// 100 repository candidate files (db_operation matches @Repository / *Repository).
	for i := 0; i < 100; i++ {
		f := "src/repository/Repo" + itoa(i) + ".java"
		q := "repository.Repo" + itoa(i)
		idx.Symbols[q] = []astpkg.SymbolDef{sym(q, "Repo"+itoa(i), "UserRepository", f, uint32(i+1), "Repository")}
		idx.Files[f] = &astpkg.FileAST{Path: f}
	}
	// 200 unrelated files (no db candidates) that must NOT be sharded.
	for i := 0; i < 200; i++ {
		f := "src/controller/Ctrl" + itoa(i) + ".java"
		idx.Files[f] = &astpkg.FileAST{Path: f}
	}
	shards := planDiscoveryShards(idx, objByType(t, "db_operation"), "")
	if len(shards) < 2 {
		t.Fatalf("100 repository candidates must shard, got %d", len(shards))
	}
	for _, sh := range shards {
		for _, f := range sh.Files {
			if !strings.HasPrefix(f, "src/repository/") {
				t.Fatalf("shard %d included non-candidate file %s", sh.Index, f)
			}
		}
	}
}

func TestMergeShardEntities_CollapsesBoundaryDupes(t *testing.T) {
	loc := []llmLocation{{File: "a.go", StartLine: 10, EndLine: 12}}
	in := [][]llmEntity{
		{{Type: "http_route", Name: "GET /x", Confidence: 0.6, Locations: loc}},
		{{Type: "http_route", Name: "GET /x", Confidence: 0.9, Locations: loc, Evidence: []llmEvidence{{}}}},
		{{Type: "http_route", Name: "GET /y", Confidence: 0.8, Locations: []llmLocation{{File: "b.go", StartLine: 1}}}},
	}
	out := mergeShardEntities(objByType(t, "http_route"), in)
	if len(out) != 2 {
		t.Fatalf("expected 2 deduped entities, got %d: %+v", len(out), out)
	}
	// The GET /x survivor must be the higher-confidence one.
	for _, e := range out {
		if e.Name == "GET /x" && e.Confidence != 0.9 {
			t.Fatalf("expected higher-confidence GET /x (0.9), got %v", e.Confidence)
		}
	}
}

func TestDistinctDirs(t *testing.T) {
	got := distinctDirs([]string{"src/api/a/X.java", "src/api/a/Y.java", "src/api/b/Z.java"})
	if len(got) != 2 || got[0] != "src/api/a" || got[1] != "src/api/b" {
		t.Fatalf("distinctDirs = %v", got)
	}
}
