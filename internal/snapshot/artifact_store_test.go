package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalArtifactStoreWrite(t *testing.T) {
	sourceRoot := t.TempDir()
	outRoot := t.TempDir()

	filePath := filepath.Join(sourceRoot, "service", "main.go")
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filePath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	inventory, err := BuildInventory(sourceRoot, InventoryOptions{})
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}

	snap := BuildSnapshot(PreparedSource{RepoLocator: sourceRoot, Ref: "WORKTREE", SourceType: "local"}, inventory)
	store := NewLocalArtifactStore(outRoot)
	if err := store.Write(context.Background(), snap, inventory, sourceRoot); err != nil {
		t.Fatalf("store.Write: %v", err)
	}

	snapshotPath := filepath.Join(outRoot, "snapshots", snap.SnapshotID, "snapshot.json")
	if _, err := os.Stat(snapshotPath); err != nil {
		t.Fatalf("expected snapshot metadata at %q: %v", snapshotPath, err)
	}

	blobPath := filepath.Join(outRoot, "blobs", inventory[0].SHA256)
	if _, err := os.Stat(blobPath); err != nil {
		t.Fatalf("expected blob at %q: %v", blobPath, err)
	}
}
