package artifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestWriteSanitizesUnresolvedFileNames(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run1",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
		OpenCodeURL:   "http://127.0.0.1:4096",
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Unresolved: []model.UnresolvedItem{
			{Kind: model.KindExposure, Type: "authentication/authorization_gap", Name: "x", ReasonCode: "gap", Reason: "missing"},
		},
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	unresolvedDir := filepath.Join(baseDir, "run1", "unresolved")
	entries, err := os.ReadDir(unresolvedDir)
	if err != nil {
		t.Fatalf("read unresolved dir failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 unresolved artifact file, got %d", len(entries))
	}
	if entries[0].IsDir() {
		t.Fatalf("expected file artifact, got directory")
	}
	if filepath.Ext(entries[0].Name()) != ".json" {
		t.Fatalf("expected .json unresolved artifact file, got %s", entries[0].Name())
	}
	if entries[0].Name() == "exposure_authentication/authorization_gap.json" {
		t.Fatalf("expected sanitized filename, got unsafe path segment")
	}
}
