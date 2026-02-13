package snapshot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildInventoryDeterministicAndClassified(t *testing.T) {
	root := t.TempDir()

	mustWriteFile(t, filepath.Join(root, "main.go"), []byte("package main\n"))
	mustWriteFile(t, filepath.Join(root, "README.md"), []byte("# test\n"))
	mustWriteFile(t, filepath.Join(root, "config.yaml"), []byte("key: value\n"))
	mustWriteFile(t, filepath.Join(root, "assets", "logo.bin"), []byte{0x00, 0x01, 0x02})
	mustWriteFile(t, filepath.Join(root, ".git", "ignored.txt"), []byte("ignore"))

	inventory, err := BuildInventory(root, InventoryOptions{ExcludeDirs: map[string]struct{}{".git": {}}})
	if err != nil {
		t.Fatalf("BuildInventory returned error: %v", err)
	}

	gotPaths := make([]string, 0, len(inventory))
	for _, entry := range inventory {
		gotPaths = append(gotPaths, entry.Path)
	}

	wantPaths := []string{"README.md", "assets/logo.bin", "config.yaml", "main.go"}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("unexpected inventory paths\nwant=%v\ngot=%v", wantPaths, gotPaths)
	}

	byPath := make(map[string]FileEntry, len(inventory))
	for _, entry := range inventory {
		byPath[entry.Path] = entry
	}

	if byPath["main.go"].Classification != "source" {
		t.Fatalf("main.go classification mismatch: %q", byPath["main.go"].Classification)
	}
	if byPath["README.md"].Classification != "doc" {
		t.Fatalf("README.md classification mismatch: %q", byPath["README.md"].Classification)
	}
	if byPath["config.yaml"].Classification != "config" {
		t.Fatalf("config.yaml classification mismatch: %q", byPath["config.yaml"].Classification)
	}
	if byPath["assets/logo.bin"].Classification != "binary" {
		t.Fatalf("assets/logo.bin classification mismatch: %q", byPath["assets/logo.bin"].Classification)
	}
}

func TestBuildSnapshotIDStable(t *testing.T) {
	prepared := PreparedSource{
		RepoLocator: "/tmp/repo",
		Ref:         "HEAD",
		CommitSHA:   "abc123",
		Workdir:     "/tmp/repo",
		SourceType:  "local-git",
	}
	inventory := []FileEntry{{
		Path:           "main.go",
		SizeBytes:      11,
		SHA256:         "ff",
		FileType:       "go",
		Classification: "source",
	}}

	a := BuildSnapshot(prepared, inventory)
	b := BuildSnapshot(prepared, inventory)

	if a.SnapshotID != b.SnapshotID {
		t.Fatalf("snapshot ids should match: %q vs %q", a.SnapshotID, b.SnapshotID)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
