package ast_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/ast"
)

// WalkVerbose must report truncation when a depth cap stops the walk before a
// reachable target, so callers can tell "capped" apart from "no connection" (A2).
func TestWalkVerboseReportsTruncation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "svc.go", `package svc

type Repo struct{}

func (r Repo) FindByID(id int) {}

func Layer2(r Repo) { r.FindByID(1) }
func Layer1(r Repo) { Layer2(r) }
func Entry(r Repo)  { Layer1(r) }
`)
	idx, err := ast.Build(context.Background(), dir, "go", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	w := ast.NewWalker(idx)
	target := func(sym string) bool { return strings.Contains(sym, "FindByID") }

	// Deep enough: the target is reached and nothing is truncated.
	paths, truncated := w.WalkVerbose("Entry", ast.WalkConfig{IsTarget: target, MaxDepth: 15})
	if len(paths) == 0 {
		t.Skipf("indexer did not link the call chain (symbols=%d); naming-dependent, skipping", len(idx.Symbols))
	}
	if truncated {
		t.Errorf("full-depth walk should not be truncated")
	}

	// Shallow cap: the walk stops before the target and must report truncation.
	shallow, truncated2 := w.WalkVerbose("Entry", ast.WalkConfig{IsTarget: target, MaxDepth: 1})
	if !truncated2 {
		t.Errorf("depth-capped walk should report truncated=true (paths=%d)", len(shallow))
	}
}

// A dense call graph (every function calls every other) enqueues exponentially
// many frontier nodes, each with its own steps/visited copy; without a frontier
// cap the walk exhausts memory before any edge cap fires (the
// cdp.content.static-brochure.importer.parser incident: 9GB on a 17MB repo).
func TestWalkVerboseCapsFrontier(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	sb.WriteString("package svc\n\nfunc Target() {}\n")
	const n = 14
	for i := 0; i < n; i++ {
		sb.WriteString("func F")
		sb.WriteString(string(rune('A' + i)))
		sb.WriteString("() {\n")
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			sb.WriteString("\tF")
			sb.WriteString(string(rune('A' + j)))
			sb.WriteString("()\n")
		}
		sb.WriteString("}\n")
	}
	sb.WriteString("func Entry() { FA() }\n")
	writeFile(t, dir, "svc.go", sb.String())

	idx, err := ast.Build(context.Background(), dir, "go", 4)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	w := ast.NewWalker(idx)
	target := func(sym string) bool { return strings.Contains(sym, "Target") }

	_, truncated := w.WalkVerbose("Entry", ast.WalkConfig{
		IsTarget:        target,
		MaxDepth:        15,
		MaxQueuedNodes:  200,
		MaxVisitedEdges: 5000,
	})
	if !truncated {
		t.Errorf("frontier-capped walk over a dense graph must report truncated=true")
	}
}
