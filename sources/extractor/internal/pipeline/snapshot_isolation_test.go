package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// TestRunPointsOpenCodeAtSnapshotNotRepo verifies that every CreateSession
// call from the orchestrator targets a directory that is NOT the user's
// repo. This is the central isolation invariant for the project: the user's
// filesystem must never be the working directory of any OpenCode session.
func TestRunPointsOpenCodeAtSnapshotNotRepo(t *testing.T) {
	repo := t.TempDir()
	// Add at least one file so the snapshot has something to mirror.
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	fake := newFakeOpenCode()

	if _, err := Run(context.Background(), cfg, repo, fake); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	dirs := fake.rec.seenDirectories()
	if len(dirs) == 0 {
		t.Fatalf("expected at least one CreateSession call, got 0")
	}
	repoAbs, _ := filepath.Abs(repo)
	for _, d := range dirs {
		if d == repo || d == repoAbs {
			t.Fatalf("OpenCode session was bound to the user's repo %q (not the snapshot)", d)
		}
		if !strings.Contains(d, "diffmind-snap-") {
			t.Fatalf("OpenCode session directory %q does not look like a snapshot", d)
		}
	}
}

// TestRunDoesNotMutateUserRepo creates a tiny repo, hashes every file in it,
// runs the pipeline, and asserts that not a single file was modified or
// added. Even the snapshot's working directory should be removed by the time
// Run returns.
func TestRunDoesNotMutateUserRepo(t *testing.T) {
	repo := t.TempDir()
	files := map[string]string{
		"main.go":        "package main\nfunc main(){}\n",
		"sub/handler.go": "package sub\n",
		"go.mod":         "module example.com/x\n",
		"config.yaml":    "service: x\n",
	}
	for rel, content := range files {
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	beforeHashes, beforeNames := hashTree(t, repo)

	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	fake := newFakeOpenCode()
	if _, err := Run(context.Background(), cfg, repo, fake); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	afterHashes, afterNames := hashTree(t, repo)
	if !equalSlices(beforeNames, afterNames) {
		t.Fatalf("file set changed.\nbefore=%v\nafter=%v", beforeNames, afterNames)
	}
	for name, h := range beforeHashes {
		if afterHashes[name] != h {
			t.Fatalf("file %s was mutated by the run", name)
		}
	}

	// Snapshot directories created during the run must be removed.
	for _, d := range fake.rec.seenDirectories() {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("snapshot %s should be removed after Run, stat=%v", d, err)
		}
	}
}

func hashTree(t *testing.T, root string) (map[string]string, []string) {
	t.Helper()
	hashes := map[string]string{}
	names := []string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(b)
		hashes[rel] = hex.EncodeToString(h[:])
		names = append(names, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hashes, names
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	am := map[string]int{}
	for _, s := range a {
		am[s]++
	}
	for _, s := range b {
		am[s]--
	}
	for _, v := range am {
		if v != 0 {
			return false
		}
	}
	return true
}
