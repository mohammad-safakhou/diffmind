package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	ctx := context.Background()
	postgresURL := os.Getenv("POSTGRES_URL")
	if postgresURL == "" {
		postgresURL = "postgres://postgres:postgres@localhost:5432/diffmind?sslmode=disable"
	}

	db, err := sql.Open("pgx", postgresURL)
	if err != nil {
		exitf("open postgres connection: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		exitf("ping postgres: %v", err)
	}

	if err := ensureSchemaMigrations(ctx, db); err != nil {
		exitf("prepare schema_migrations table: %v", err)
	}

	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		exitf("load migration files: %v", err)
	}
	sort.Strings(files)

	for _, file := range files {
		name := filepath.Base(file)
		applied, err := isMigrationApplied(ctx, db, name)
		if err != nil {
			exitf("check migration %s: %v", name, err)
		}
		if applied {
			fmt.Printf("skip %s (already applied)\n", name)
			continue
		}

		content, err := os.ReadFile(file)
		if err != nil {
			exitf("read migration %s: %v", name, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			exitf("begin tx for migration %s: %v", name, err)
		}

		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			exitf("execute migration %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name) VALUES ($1)`, name,
		); err != nil {
			_ = tx.Rollback()
			exitf("record migration %s: %v", name, err)
		}
		if err := tx.Commit(); err != nil {
			exitf("commit migration %s: %v", name, err)
		}

		fmt.Printf("applied %s\n", name)
	}
}

func ensureSchemaMigrations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

func isMigrationApplied(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRowContext(ctx, `SELECT name FROM schema_migrations WHERE name = $1`, name).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return strings.EqualFold(found, name), nil
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
