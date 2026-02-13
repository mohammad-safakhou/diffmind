package snapshot

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"diffmind/internal/config"
)

func Run(ctx context.Context, args []string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}

	resolver := NewSourceResolver()
	prepared, cleanup, err := resolver.Prepare(ctx, opts.Source, opts.Ref)
	if err != nil {
		return err
	}
	defer func() {
		if cleanErr := cleanup(); cleanErr != nil {
			slog.Warn("snapshot cleanup failed", "error", cleanErr)
		}
	}()

	inventory, err := BuildInventory(prepared.Workdir, InventoryOptions{
		ExcludeDirs: map[string]struct{}{
			".git":      {},
			".diffmind": {},
			".gocache":  {},
			"bin":       {},
		},
	})
	if err != nil {
		return err
	}

	snap := BuildSnapshot(prepared, inventory)
	artifactRoot := filepath.Join(opts.OutDir, "artifacts")
	store := NewLocalArtifactStore(artifactRoot)
	if err := store.Write(ctx, snap, inventory, prepared.Workdir); err != nil {
		return err
	}
	if opts.Persist {
		cfg, err := config.LoadFromEnv()
		if err != nil {
			return fmt.Errorf("load persistence config: %w", err)
		}
		if err := PersistSnapshotArtifacts(ctx, cfg, prepared, snap, inventory); err != nil {
			return err
		}
	}

	slog.Info("snapshot completed",
		"snapshot_id", snap.SnapshotID,
		"files", snap.FileCount,
		"repo", snap.RepoLocator,
		"ref", snap.Ref,
		"commit", snap.CommitSHA,
		"artifact_root", artifactRoot,
	)
	fmt.Fprintf(os.Stdout, "%s\n", snap.SnapshotID)
	return nil
}

func parseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)

	source := fs.String("source", ".", "Repository source (local path or remote URL)")
	ref := fs.String("ref", "HEAD", "Git ref to snapshot")
	outDir := fs.String("out", ".diffmind", "Output root for snapshot artifacts")
	persist := fs.Bool("persist", false, "Persist metadata to Postgres and blobs to MinIO")

	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("parse snapshot flags: %w", err)
	}

	return Options{Source: *source, Ref: *ref, OutDir: *outDir, Persist: *persist}, nil
}
