package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/workspace/orchestrator"
	"github.com/mohammad-safakhou/diffmind/internal/workspace/store"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cleanTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runGitForTest(t, root, "init", "-b", "master")
	runGitForTest(t, root, "config", "user.email", "developer@example.test")
	runGitForTest(t, root, "config", "user.name", "Developer")
	writeTestFile(t, filepath.Join(root, "main.go"), "package main\nfunc main() {}\n")
	runGitForTest(t, root, "add", ".")
	runGitForTest(t, root, "commit", "-m", "fixture")
	return root
}

func TestAnalysisFingerprintInvalidation(t *testing.T) {
	tests := []struct {
		name        string
		change      func(*testing.T, *Server, string, *store.Repo, *orchestrator.DiffMindRunOptions)
		uncacheable bool
	}{
		{"commit", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			writeTestFile(t, filepath.Join(r.Path, "new.go"), "package main")
			runGitForTest(t, r.Path, "add", ".")
			runGitForTest(t, r.Path, "commit", "-m", "change")
		}, false},
		{"dirty", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			writeTestFile(t, filepath.Join(r.Path, "main.go"), "package changed")
		}, true},
		{"untracked", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			writeTestFile(t, filepath.Join(r.Path, "other.go"), "package main")
		}, true},
		{"central config", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			writeTestFile(t, filepath.Join(s.store.HomeDir(), "config.json"), `{"quality":{"min_confidence":0.8}}`)
		}, false},
		{"explicit config", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			o.ConfigPath = filepath.Join(t.TempDir(), "config.json")
			writeTestFile(t, o.ConfigPath, `{}`)
		}, false},
		{"project pack", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			writeTestFile(t, filepath.Join(s.store.PacksDir(pid), "custom.json"), `{"id":"custom"}`)
		}, false},
		{"pack selection", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			r.PackIDs = []string{"custom"}
		}, false},
		{"project config", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			s.store.UpdateProject(pid, func(p *store.Project) { p.Instruction = "updated" })
		}, false},
		{"options", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			o.Workers = 2
		}, false},
		{"version", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			s.SetVersion("new")
		}, false},
		{"invalid lock", func(t *testing.T, s *Server, pid string, r *store.Repo, o *orchestrator.DiffMindRunOptions) {
			writeTestFile(t, filepath.Join(s.store.HomeDir(), "diffmind-packs.lock"), "version: 999")
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newAuthTestServer(t)
			p, _ := s.store.CreateProject(store.Project{Name: "Inputs"})
			r := store.Repo{Name: "service", Path: cleanTestRepo(t)}
			opts := orchestrator.DiffMindRunOptions{}
			before, err := s.analysisFingerprint(context.Background(), p.ID, r, opts, "engine")
			if err != nil || before == "" {
				t.Fatalf("before: %q %v", before, err)
			}
			again, _ := s.analysisFingerprint(context.Background(), p.ID, r, opts, "engine")
			if again != before {
				t.Fatal("unchanged inputs are not stable")
			}
			tc.change(t, s, p.ID, &r, &opts)
			after, err := s.analysisFingerprint(context.Background(), p.ID, r, opts, "engine")
			if tc.uncacheable {
				if err == nil || after != "" {
					t.Fatalf("unsafe cache key: %q %v", after, err)
				}
				return
			}
			if err != nil || after == "" || before == after {
				t.Fatalf("not invalidated: %q %v", after, err)
			}
		})
	}
}

func TestAnalysisFingerprintIncludesIgnoredServiceHints(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Hints"})
	r := store.Repo{Path: cleanTestRepo(t)}
	writeTestFile(t, filepath.Join(r.Path, ".gitignore"), ".diffmind/\ndiffmind-configuration.yaml\napplication.yaml\n")
	runGitForTest(t, r.Path, "add", ".gitignore")
	runGitForTest(t, r.Path, "commit", "-m", "ignore local hints")
	before, err := s.analysisFingerprint(context.Background(), p.ID, r, orchestrator.DiffMindRunOptions{}, "engine")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".diffmind/service.yaml", "diffmind-configuration.yaml", "application.yaml"} {
		writeTestFile(t, filepath.Join(r.Path, path), "service:\n  name: renamed\n")
		after, err := s.analysisFingerprint(context.Background(), p.ID, r, orchestrator.DiffMindRunOptions{}, "engine")
		if err != nil || after == before {
			t.Fatalf("hint %s not invalidated: %v", path, err)
		}
		before = after
	}
}

func TestAnalysisFingerprintRejectsSymlinkAndUnknownAnalyzer(t *testing.T) {
	s := newAuthTestServer(t)
	p, _ := s.store.CreateProject(store.Project{Name: "Safety"})
	r := store.Repo{Path: cleanTestRepo(t)}
	if fingerprint, err := s.analysisFingerprint(context.Background(), p.ID, r, orchestrator.DiffMindRunOptions{}, ""); err == nil || fingerprint != "" {
		t.Fatal("unknown analyzer was cacheable")
	}
	if err := os.Symlink("main.go", filepath.Join(r.Path, "linked.go")); err != nil {
		t.Skip(err)
	}
	runGitForTest(t, r.Path, "add", ".")
	runGitForTest(t, r.Path, "commit", "-m", "symlink")
	if fingerprint, err := s.analysisFingerprint(context.Background(), p.ID, r, orchestrator.DiffMindRunOptions{}, "engine"); err == nil || fingerprint != "" {
		t.Fatal("symlink checkout was cacheable")
	}
}
