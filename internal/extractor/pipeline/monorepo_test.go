package pipeline

import (
	"context"
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

func TestASTIndexScopesMonorepoRunToRequestedSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	serviceA := filepath.Join(root, "services", "a")
	serviceB := filepath.Join(root, "services", "b")
	if err := os.MkdirAll(serviceA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(serviceB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serviceA, "main.go"), []byte("package main\nfunc ServiceA() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serviceB, "app.py"), []byte("def service_b():\n    pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sourceRoot, subDir := detectMonorepo(serviceA)
	o := &orchestrator{repoPath: serviceA, sourceRoot: sourceRoot, subDir: subDir}
	if err := o.runASTIndexStage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o.astIndex == nil || len(o.astIndex.Files) != 1 {
		t.Fatalf("index escaped requested service: %+v", o.astIndex)
	}
	for _, file := range o.astIndex.Files {
		if file.Language != "go" {
			t.Fatalf("index escaped requested service: %+v", o.astIndex)
		}
	}
}
