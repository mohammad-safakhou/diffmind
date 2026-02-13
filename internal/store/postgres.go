package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type FileInventoryRow struct {
	Path           string
	SizeBytes      int64
	SHA256         string
	FileType       string
	Classification string
}

type SnapshotMetadataRecord struct {
	SnapshotKey string
	RepoLocator string
	Ref         string
	CommitSHA   string
}

type PostgresSnapshotStore struct {
	db *sql.DB
}

func NewPostgresSnapshotStore(ctx context.Context, postgresURL string) (*PostgresSnapshotStore, error) {
	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(15 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &PostgresSnapshotStore{db: db}, nil
}

func (s *PostgresSnapshotStore) PersistSnapshot(ctx context.Context, snap SnapshotMetadataRecord, inventory []FileInventoryRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin postgres tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var runID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO runs (run_type, status) VALUES ($1, $2) RETURNING id`,
		"snapshot", "completed",
	).Scan(&runID); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}

	var snapshotID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO snapshots (run_id, repo_locator, git_ref, commit_sha, snapshot_key)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (snapshot_key)
		DO UPDATE SET
			run_id = EXCLUDED.run_id,
			repo_locator = EXCLUDED.repo_locator,
			git_ref = EXCLUDED.git_ref,
			commit_sha = EXCLUDED.commit_sha
		RETURNING id
	`, runID, snap.RepoLocator, snap.Ref, nullIfEmpty(snap.CommitSHA), snap.SnapshotKey).Scan(&snapshotID); err != nil {
		return fmt.Errorf("upsert snapshot: %w", err)
	}

	insertInventory := `
		INSERT INTO file_inventory (snapshot_id, path, size_bytes, sha256, file_type, classification)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (snapshot_id, path)
		DO UPDATE SET
			size_bytes = EXCLUDED.size_bytes,
			sha256 = EXCLUDED.sha256,
			file_type = EXCLUDED.file_type,
			classification = EXCLUDED.classification
	`
	for _, file := range inventory {
		if _, err := tx.ExecContext(ctx, insertInventory,
			snapshotID,
			file.Path,
			file.SizeBytes,
			file.SHA256,
			nullIfEmpty(file.FileType),
			nullIfEmpty(file.Classification),
		); err != nil {
			return fmt.Errorf("insert inventory row for %q: %w", file.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit postgres tx: %w", err)
	}

	return nil
}

func (s *PostgresSnapshotStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}
