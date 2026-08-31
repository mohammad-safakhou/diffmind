package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeManifest(t *testing.T, runsDir, runID, repoPath string, started time.Time) {
	t.Helper()
	dir := filepath.Join(runsDir, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{"run_id": runID, "repo_path": repoPath, "started_at": started.Format(time.RFC3339Nano)}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "run_manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverDiffMindRuns(t *testing.T) {
	runsDir := t.TempDir()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	writeManifest(t, runsDir, "20260501T100000Z", "/repo/a", base)
	writeManifest(t, runsDir, "20260502T100000Z", "/repo/a", base.Add(24*time.Hour))
	writeManifest(t, runsDir, "20260501T120000Z", "/repo/b", base.Add(2*time.Hour))

	// A malformed run must be skipped, not fatal.
	bad := filepath.Join(runsDir, "20260503T100000Z")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "run_manifest.json"), []byte("{not json"), 0o644)
	// A run with no manifest is also skipped.
	os.MkdirAll(filepath.Join(runsDir, "20260504T100000Z"), 0o755)

	runs, err := DiscoverDiffMindRuns(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("expected 3 valid runs, got %d", len(runs))
	}
	// Newest first.
	if runs[0].RunID != "20260502T100000Z" {
		t.Fatalf("newest first failed: %+v", runs)
	}

	byRepo, err := DiscoverDiffMindRunsByRepo(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(byRepo["/repo/a"]) != 2 || len(byRepo["/repo/b"]) != 1 {
		t.Fatalf("grouping wrong: %+v", byRepo)
	}
	if byRepo["/repo/a"][0].RunID != "20260502T100000Z" {
		t.Fatalf("latest for /repo/a wrong: %+v", byRepo["/repo/a"])
	}

	latest, ok, err := LatestDiffMindRunForRepo(runsDir, "/repo/a")
	if err != nil || !ok {
		t.Fatalf("latest lookup failed: ok=%v err=%v", ok, err)
	}
	if latest.RunID != "20260502T100000Z" {
		t.Fatalf("latest = %s", latest.RunID)
	}

	if _, ok, _ := LatestDiffMindRunForRepo(runsDir, "/nope"); ok {
		t.Fatal("expected no run for unknown repo")
	}
}

func TestDiscoverMissingDir(t *testing.T) {
	runs, err := DiscoverDiffMindRuns(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if runs != nil {
		t.Fatalf("expected nil runs, got %v", runs)
	}
}

func TestRunMatchesRepo(t *testing.T) {
	if !RunMatchesRepo(DiffMindRunInfo{RepoPath: "/old/worktrees/routing-service"}, "routing-service", "repo-1", "/new/worktrees/routing-service") {
		t.Fatal("expected matching repo basename to preserve moved checkout run")
	}
	if !RunMatchesRepo(DiffMindRunInfo{ServiceID: "service.catalogue_service"}, "checkout-service", "repo-2", "/new/checkout/fma") {
		t.Fatal("expected DiffMind protocol service identity to match repo")
	}
	if RunMatchesRepo(DiffMindRunInfo{RepoPath: "/repo/gateway-service", ServiceID: "gateway-service"}, "routing-service", "repo-3", "/repo/routing-service") {
		t.Fatal("expected mismatched service identity to be rejected")
	}
}
