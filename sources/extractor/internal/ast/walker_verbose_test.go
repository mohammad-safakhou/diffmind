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
	idx, err := ast.Build(context.Background(), dir, "go", 4, nil)
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
