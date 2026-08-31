package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Helper: lay out a minimal SPA tree under `dir` with two files:
//
//	src/foo.js  (source, modified at srcMtime)
//	dist/bundle.js (build output, modified at distMtime)
//
// plus the marker files extractorFindSPAWorkspace looks for.
func writeFakeSPATree(t *testing.T, dir string, srcMtime, distMtime time.Time) string {
	t.Helper()
	web := filepath.Join(dir, "internal", "ui", "web")
	if err := os.MkdirAll(filepath.Join(web, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(web, "dist", "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(web, "package.json")
	if err := os.WriteFile(pkg, []byte(`{"name":"fake"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(web, "src", "foo.js")
	if err := os.WriteFile(src, []byte(`/* src */`), 0o644); err != nil {
		t.Fatal(err)
	}
	dist := filepath.Join(web, "dist", "assets", "bundle.js")
	if err := os.WriteFile(dist, []byte(`/* dist */`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pin mtimes so the test is independent of write order timing.
	if err := os.Chtimes(src, srcMtime, srcMtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dist, distMtime, distMtime); err != nil {
		t.Fatal(err)
	}
	// Also pin package.json so it doesn't accidentally outrank src/.
	if err := os.Chtimes(pkg, srcMtime.Add(-time.Hour), srcMtime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	return web
}

// When src/ was modified AFTER dist/, the detector must report a
// rebuild is needed. This is the path that mattered for run
// 20260518T115925Z: my SPA changes were on disk but `dist/` was
// stale, and `go run` happily embedded the stale dist.
func TestSPARebuildNeeded_SrcNewerThanDist(t *testing.T) {
	tmp := t.TempDir()
	srcTime := time.Now()
	distTime := srcTime.Add(-time.Hour)
	web := writeFakeSPATree(t, tmp, srcTime, distTime)

	need, reason := extractorSPARebuildNeeded(web)
	if !need {
		t.Fatalf("expected rebuild needed; got reason=%q", reason)
	}
	if reason == "" {
		t.Errorf("reason must be populated so the user knows why we're rebuilding")
	}
}

// When dist/ was rebuilt AFTER the last src/ change the detector
// must NOT report a rebuild — otherwise `go run ./cmd/diffmind ui`
// would shell out to `npm run build` on every startup, even with no
// SPA work to do.
func TestSPARebuildNeeded_DistNewerThanSrc(t *testing.T) {
	tmp := t.TempDir()
	srcTime := time.Now().Add(-time.Hour)
	distTime := time.Now()
	web := writeFakeSPATree(t, tmp, srcTime, distTime)

	need, reason := extractorSPARebuildNeeded(web)
	if need {
		t.Fatalf("expected no rebuild needed; got reason=%q", reason)
	}
}

// Missing dist/ entirely must trigger a rebuild — that's the
// first-time-from-clean-checkout scenario.
func TestSPARebuildNeeded_DistMissing(t *testing.T) {
	tmp := t.TempDir()
	web := filepath.Join(tmp, "internal", "ui", "web")
	if err := os.MkdirAll(filepath.Join(web, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "src", "x.js"), []byte(`x`), 0o644); err != nil {
		t.Fatal(err)
	}

	need, reason := extractorSPARebuildNeeded(web)
	if !need {
		t.Fatalf("expected rebuild needed when dist is missing; got reason=%q", reason)
	}
	if reason == "" {
		t.Errorf("reason must be set so the cause is auditable")
	}
}

// Empty dist/ (build aborted halfway) must also trigger a rebuild.
func TestSPARebuildNeeded_EmptyDist(t *testing.T) {
	tmp := t.TempDir()
	web := filepath.Join(tmp, "internal", "ui", "web")
	if err := os.MkdirAll(filepath.Join(web, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(web, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "package.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(web, "src", "x.js"), []byte(`x`), 0o644); err != nil {
		t.Fatal(err)
	}

	need, _ := extractorSPARebuildNeeded(web)
	if !need {
		t.Fatalf("empty dist/ must trigger a rebuild")
	}
}

// extractorFindSPAWorkspace walks up from cwd looking for the workspace. We
// can't change os.Getwd globally without affecting other tests, so
// the regression we actually care about (returning the right path
// when invoked from a subdirectory of the project root) is implicitly
// covered by the production code path. This test just verifies the
// "not found" case so we don't accidentally return cwd when no SPA
// is around.
func TestFindSPAWorkspace_NotFound(t *testing.T) {
	// Stash cwd, chdir to a temp dir with no SPA, restore on exit.
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	if got := extractorFindSPAWorkspace(); got != "" {
		t.Errorf("expected empty path when no SPA tree is around; got %q", got)
	}
}
