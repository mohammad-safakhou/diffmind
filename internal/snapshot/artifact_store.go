package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type LocalArtifactStore struct {
	RootDir string
}

func NewLocalArtifactStore(rootDir string) LocalArtifactStore {
	return LocalArtifactStore{RootDir: rootDir}
}

func (s LocalArtifactStore) Write(_ context.Context, snap Snapshot, inventory []FileEntry, sourceRoot string) error {
	if err := os.MkdirAll(s.RootDir, 0o755); err != nil {
		return fmt.Errorf("create artifact root dir: %w", err)
	}

	blobsDir := filepath.Join(s.RootDir, "blobs")
	if err := os.MkdirAll(blobsDir, 0o755); err != nil {
		return fmt.Errorf("create blobs dir: %w", err)
	}

	for _, file := range inventory {
		if err := s.persistBlob(blobsDir, sourceRoot, file); err != nil {
			return err
		}
	}

	snapshotDir := filepath.Join(s.RootDir, "snapshots", snap.SnapshotID)
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return fmt.Errorf("create snapshot dir: %w", err)
	}

	if err := writeJSON(filepath.Join(snapshotDir, "snapshot.json"), snap); err != nil {
		return fmt.Errorf("write snapshot metadata: %w", err)
	}
	if err := writeJSON(filepath.Join(snapshotDir, "inventory.json"), inventory); err != nil {
		return fmt.Errorf("write inventory: %w", err)
	}

	return nil
}

func (s LocalArtifactStore) persistBlob(blobsDir string, sourceRoot string, file FileEntry) error {
	blobPath := filepath.Join(blobsDir, file.SHA256)
	if _, err := os.Stat(blobPath); err == nil {
		return nil
	}

	sourcePath := filepath.Join(sourceRoot, filepath.FromSlash(file.Path))
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", sourcePath, err)
	}
	defer src.Close()

	tmpPath := blobPath + ".tmp"
	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create blob temp file %q: %w", tmpPath, err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy blob content for %q: %w", file.Path, err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close blob temp file %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, blobPath); err != nil {
		return fmt.Errorf("persist blob %q: %w", blobPath, err)
	}

	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
