package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
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
