package artifacts

import (
	"os"
	"path/filepath"
	"testing"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Join(wd, "..", "..", "testdata")
}

func TestReadDiffMindArtifacts_OrderService(t *testing.T) {
	td := testdataDir(t)
	artifactsDir := filepath.Join(td, "sample_diffmind_output", "order-service", ".diffmind", "runs", "run_001")

	arch, err := ReadDiffMindArtifacts(artifactsDir)
	if err != nil {
		t.Fatalf("failed to read artifacts: %v", err)
	}

	if arch.Manifest == nil {
		t.Fatal("expected manifest to be loaded")
	}
	if arch.Manifest.RunID != "run_001" {
		t.Errorf("expected run_id run_001, got %s", arch.Manifest.RunID)
	}

	if len(arch.Exposures) != 2 {
		t.Errorf("expected 2 exposures, got %d", len(arch.Exposures))
	}
	if len(arch.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies (1 outbound_http + 1 db_operation), got %d", len(arch.Dependencies))
	}
	if len(arch.Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(arch.Connections))
	}
}

func TestReadDiffMindArtifacts_BillingService(t *testing.T) {
	td := testdataDir(t)
	artifactsDir := filepath.Join(td, "sample_diffmind_output", "billing-service", ".diffmind", "runs", "run_001")

	arch, err := ReadDiffMindArtifacts(artifactsDir)
	if err != nil {
		t.Fatalf("failed to read artifacts: %v", err)
	}

	if len(arch.Exposures) != 1 {
		t.Errorf("expected 1 exposure, got %d", len(arch.Exposures))
	}
	if len(arch.Dependencies) != 2 {
		t.Errorf("expected 2 dependencies, got %d", len(arch.Dependencies))
	}
	if len(arch.Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(arch.Connections))
	}

	// Verify specific data.
	if arch.Exposures[0].Name != "POST /api/charge" {
		t.Errorf("expected exposure name 'POST /api/charge', got %s", arch.Exposures[0].Name)
	}
}

func TestReadDiffMindRun_LatestRun(t *testing.T) {
	td := testdataDir(t)
	repoPath := filepath.Join(td, "sample_diffmind_output", "order-service")

	arch, err := ReadDiffMindRun(repoPath)
	if err != nil {
		t.Fatalf("failed to read latest run: %v", err)
	}

	if arch.Manifest == nil {
		t.Fatal("expected manifest")
	}
	if len(arch.Exposures) != 2 {
		t.Errorf("expected 2 exposures, got %d", len(arch.Exposures))
	}
}

func TestReadDiffMindArtifacts_NonExistentDir(t *testing.T) {
	_, err := ReadDiffMindArtifacts("/nonexistent/path")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}
