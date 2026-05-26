package agents

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/indexer"
)

// captureIndexer records the RunRequest it was handed without doing
// any real work. We use it to assert that the index stage passes
// absolute paths to the indexer driver, regardless of whether the
// caller supplied a relative RunDir.
type captureIndexer struct {
	mu  sync.Mutex
	req indexer.RunRequest
}

type failingIndexer struct{}

func (f failingIndexer) Index(_ context.Context, _ indexer.RunRequest) (*indexer.RunResult, error) {
	return &indexer.RunResult{
		SchemaVersion: indexer.ReportSchemaVersion,
	}, errors.New("indexer container exited with code 1")
}

// Index implements indexer.Indexer. It returns an empty (but valid)
// RunResult so the index stage proceeds to load the (nonexistent)
// SCIP index — that gracefully degrades inside the stage and is fine
// for this regression test, which only cares about the request shape.
func (c *captureIndexer) Index(_ context.Context, req indexer.RunRequest) (*indexer.RunResult, error) {
	c.mu.Lock()
	c.req = req
	c.mu.Unlock()
	return &indexer.RunResult{
		SchemaVersion: indexer.ReportSchemaVersion,
	}, nil
}

// TestRunIndexStagePassesAbsolutePathsToIndexer is the regression
// guard for the production bug that surfaced in run 20260524T212718Z:
//
//	"output path must be absolute: \".diffmind/runs/...\""
//
// The diffmind CLI defaults Artifacts.BaseDir to ".diffmind/runs",
// so the RunDir handed to the orchestrator is RELATIVE. The docker
// indexer's `validateRequest` correctly rejects relative volume mount
// paths, the index stage emitted job_failed, and connections degraded
// to the empty shallow matcher — producing zero connections for a
// 68-exposure run.
//
// The fix: `runIndexStage` now resolves both indexDir and sourceDir
// via filepath.Abs before constructing the indexer.RunRequest. This
// test pins that contract.
func TestRunIndexStagePassesAbsolutePathsToIndexer(t *testing.T) {
	// Relative RunDir, exactly how the CLI calls us.
	tmp := t.TempDir() // already absolute
	relRunDir := "./relative/run/dir"
	cap := &captureIndexer{}

	// Build a minimal orchestrator stub directly; we don't need the
	// full pipeline for this unit test. The stage only reads cfg,
	// runDir, sessionDir, sink, and indexerOverride.
	o := &orchestrator{
		cfg:             config.Default(),
		runDir:          relRunDir,
		sessionDir:      tmp,
		indexerOverride: cap,
	}

	if err := o.runIndexStage(context.Background()); err != nil {
		t.Fatalf("runIndexStage: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()

	if !filepath.IsAbs(cap.req.OutputPath) {
		t.Errorf("OutputPath must be absolute, got %q", cap.req.OutputPath)
	}
	if !filepath.IsAbs(cap.req.SourcePath) {
		t.Errorf("SourcePath must be absolute, got %q", cap.req.SourcePath)
	}
	// And the absolute output dir should still be a child of the
	// relative-input dir, just resolved against the working directory.
	wantSuffix := filepath.Join("relative", "run", "dir", "index")
	if !filepath.IsAbs(cap.req.OutputPath) ||
		filepath.Base(filepath.Dir(cap.req.OutputPath)) != "dir" ||
		filepath.Base(cap.req.OutputPath) != "index" {
		t.Errorf("OutputPath should resolve %q to an absolute path ending in %q, got %q",
			relRunDir, wantSuffix, cap.req.OutputPath)
	}
}

// TestRunIndexStageSkipsWhenDisabled documents the fail-soft path
// for environments without Docker (CI, air-gapped). Setting
// Indexer.Disabled bypasses the stage entirely; the connections
// stage then falls back to the shallow matcher.
func TestRunIndexStageSkipsWhenDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Indexer.Disabled = true

	cap := &captureIndexer{}
	o := &orchestrator{
		cfg:             cfg,
		runDir:          t.TempDir(),
		sessionDir:      t.TempDir(),
		indexerOverride: cap,
	}
	if err := o.runIndexStage(context.Background()); err != nil {
		t.Fatalf("runIndexStage: %v", err)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.req.SourcePath != "" {
		t.Errorf("expected indexer NOT to be called when disabled; got req=%+v", cap.req)
	}
}

// TestRunIndexStageHaltsOnImageBuildFailure is the Sprint 4
// follow-up regression: when the parallel image build fails, the
// index stage MUST return a non-nil error so the orchestrator can
// halt the whole pipeline (fail-fast policy).
//
// Background: run 20260525T115727Z built the indexer image with a
// broken scip-java URL (404 piped into `tar -xz` → "gzip: stdin:
// not in gzip format"), which used to fail-soft and let the LLM
// stages keep running for ~8 more minutes producing useless
// output. We changed the policy: image build failure halts the
// run.
func TestRunIndexStageHaltsOnImageBuildFailure(t *testing.T) {
	cfg := config.Default()
	o := &orchestrator{
		cfg:        cfg,
		runDir:     t.TempDir(),
		sessionDir: t.TempDir(),
	}
	// Simulate the parallel build having already failed: a
	// pre-closed buildDone with a non-nil buildResult.Err.
	o.buildDone = make(chan struct{})
	close(o.buildDone)
	o.buildResult = buildOutcome{
		Err:   errImageBuildFailed,
		Image: "diffmind-indexer:java21",
	}

	err := o.runIndexStage(context.Background())
	if err == nil {
		t.Fatalf("expected runIndexStage to return non-nil error on build failure")
	}
	// The error must wrap the underlying build error so the
	// dashboard's failure report shows the user the real cause.
	if !errorContains(err, "indexer image build failed") {
		t.Errorf("error %q should mention 'indexer image build failed'", err)
	}
}

func TestRunIndexStageHaltsOnIndexerRuntimeFailure(t *testing.T) {
	o := &orchestrator{
		cfg:             config.Default(),
		runDir:          t.TempDir(),
		sessionDir:      t.TempDir(),
		indexerOverride: failingIndexer{},
	}

	err := o.runIndexStage(context.Background())
	if err == nil {
		t.Fatalf("expected runIndexStage to fail when configured indexer execution fails")
	}
	if !errorContains(err, "indexer failed") {
		t.Errorf("error %q should mention 'indexer failed'", err)
	}
}

// errImageBuildFailed is a fixture error used by the fail-fast test.
var errImageBuildFailed = &simpleErr{msg: "scip-java download failed: HTTP 404"}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func errorContains(err error, substr string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
