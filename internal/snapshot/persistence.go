package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"diffmind/internal/config"
	"diffmind/internal/store"
)

func PersistSnapshotArtifacts(ctx context.Context, cfg config.Config, prepared PreparedSource, snap Snapshot, inventory []FileEntry) error {
	metaStore, err := store.NewPostgresSnapshotStore(ctx, cfg.PostgresURL)
	if err != nil {
		return err
	}
	defer func() {
		_ = metaStore.Close()
	}()

	rows := make([]store.FileInventoryRow, 0, len(inventory))
	for _, file := range inventory {
		rows = append(rows, store.FileInventoryRow{
			Path:           file.Path,
			SizeBytes:      file.SizeBytes,
			SHA256:         file.SHA256,
			FileType:       file.FileType,
			Classification: file.Classification,
		})
	}

	if err := metaStore.PersistSnapshot(ctx, store.SnapshotMetadataRecord{
		SnapshotKey: snap.SnapshotID,
		RepoLocator: snap.RepoLocator,
		Ref:         snap.Ref,
		CommitSHA:   snap.CommitSHA,
	}, rows); err != nil {
		return fmt.Errorf("persist snapshot metadata to postgres: %w", err)
	}

	blobStore, err := store.NewMinIOBlobStore(cfg.MinIOURL, cfg.MinIOUser, cfg.MinIOPass, cfg.S3Bucket)
	if err != nil {
		return err
	}
	if err := blobStore.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("ensure minio bucket: %w", err)
	}

	repoHash := hashString(snap.RepoLocator)
	commit := snap.CommitSHA
	if commit == "" {
		commit = "worktree"
	}

	for _, file := range inventory {
		sourcePath := filepath.Join(prepared.Workdir, filepath.FromSlash(file.Path))
		objectKey := fmt.Sprintf("repos/%s/%s/%s", repoHash, commit, file.SHA256)
		if err := blobStore.PutFile(ctx, objectKey, sourcePath); err != nil {
			return fmt.Errorf("persist blob for %q: %w", file.Path, err)
		}
	}

	return nil
}

func hashString(value string) string {
	h := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(h[:])
}
