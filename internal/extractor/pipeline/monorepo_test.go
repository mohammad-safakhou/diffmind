package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMonorepoStandaloneRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, sub := detectMonorepo(dir)
	if s != dir {
		t.Fatalf("expected session dir to equal input when repoPath has .git, got %q", s)
	}
	if sub != "" {
		t.Fatalf("expected empty subdir, got %q", sub)
	}
}

func TestDetectMonorepoNestedSubdir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "services", "users-api")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	s, sub := detectMonorepo(target)
	absRoot, _ := filepath.EvalSymlinks(root)
	absS, _ := filepath.EvalSymlinks(s)
	if absS != absRoot {
		t.Fatalf("expected session dir to be git root %q, got %q", absRoot, absS)
	}
	if sub != filepath.Join("services", "users-api") {
		t.Fatalf("expected sub dir 'services/users-api', got %q", sub)
	}
}

func TestDetectMonorepoNoGitRoot(t *testing.T) {
	dir := t.TempDir()
	s, sub := detectMonorepo(dir)
	if s != dir || sub != "" {
		t.Fatalf("expected fallback (repoPath, '') when no .git found, got (%q, %q)", s, sub)
	}
}
