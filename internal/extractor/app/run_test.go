package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/config"
)

func TestRunDeterministicPipelineDoesNotRequireExternalProvider(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "app.py"), []byte(`
from flask import Flask

app = Flask(__name__)

@app.route("/ready")
def ready():
    return "ok"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Runtime.Pipeline = config.PipelineDeterministic
	cfg.Artifacts.BaseDir = t.TempDir()

	out, err := Run(context.Background(), RunInput{RepoPath: repo, Config: cfg, RunID: "deterministic-test"})
	if err != nil {
		t.Fatalf("deterministic run failed: %v", err)
	}
	if out.RunID != "deterministic-test" || out.RunDir == "" {
		t.Fatalf("unexpected run output: %+v", out)
	}
}

func TestAllocateDefaultRunDirUniqueWithinSameNanosecond(t *testing.T) {
	base := t.TempDir()
	started := time.Date(2026, 6, 30, 23, 8, 15, 123456789, time.UTC)
	id1, dir1, err := allocateDefaultRunDir(base, started)
	if err != nil {
		t.Fatalf("first allocation failed: %v", err)
	}
	id2, dir2, err := allocateDefaultRunDir(base, started)
	if err != nil {
		t.Fatalf("second allocation failed: %v", err)
	}
	if id1 == id2 || dir1 == dir2 {
		t.Fatalf("allocations collided: %q/%q and %q/%q", id1, dir1, id2, dir2)
	}
	if _, err := os.Stat(dir1); err != nil {
		t.Fatalf("first dir not created: %v", err)
	}
	if _, err := os.Stat(dir2); err != nil {
		t.Fatalf("second dir not created: %v", err)
	}
}
