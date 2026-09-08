package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/artifacts"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func TestLocalDiffMindFreshnessTracksCheckoutAndDirtyState(t *testing.T) {
	dir := t.TempDir()
	runGitForTest(t, dir, "init")
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	path := filepath.Join(dir, "app.py")
	if err := os.WriteFile(path, []byte("print('one')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, dir, "add", "app.py")
	runGitForTest(t, dir, "commit", "-m", "one")
	head := gitOutput(t.Context(), dir, "rev-parse", "HEAD")
	repo := store.Repo{Path: dir, SourceType: "local"}
	latest := &artifacts.DiffMindRunInfo{RepoGitSHA: head}
	if got := diffmindFreshness(repo, latest); got != "fresh" {
		t.Fatalf("clean checkout freshness = %q", got)
	}
	if err := os.WriteFile(path, []byte("print('dirty')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := diffmindFreshness(repo, latest); got != "dirty" {
		t.Fatalf("dirty checkout freshness = %q", got)
	}
	runGitForTest(t, dir, "add", "app.py")
	runGitForTest(t, dir, "commit", "-m", "two")
	if got := diffmindFreshness(repo, latest); got != "stale" {
		t.Fatalf("advanced checkout freshness = %q", got)
	}
}
