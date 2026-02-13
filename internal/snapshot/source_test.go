package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareLocalNonGit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("ok\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	resolver := NewSourceResolver()
	prepared, cleanup, err := resolver.Prepare(context.Background(), root, "HEAD")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	defer func() {
		_ = cleanup()
	}()

	if prepared.Workdir != root {
		t.Fatalf("expected workdir=%q got=%q", root, prepared.Workdir)
	}
	if prepared.Ref != "WORKTREE" {
		t.Fatalf("expected ref WORKTREE, got %q", prepared.Ref)
	}
	if prepared.SourceType != "local" {
		t.Fatalf("expected source type local, got %q", prepared.SourceType)
	}
}
