package artifacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

func TestWriteGraph(t *testing.T) {
	tmpDir := t.TempDir()

	g := &model.CrossServiceGraph{
		Version:     "v1alpha1",
		GeneratedAt: time.Now().UTC(),
		Services: []model.GraphNode{
			{ID: "svc1", Name: "order-service"},
			{ID: "svc2", Name: "billing-service"},
		},
		Edges: []model.GraphEdge{
			{
				ID:          "edge1",
				FromService: "order-service",
				ToService:   "billing-service",
				Type:        "http",
				Confidence:  0.85,
			},
		},
	}

	outputDir, err := WriteGraph(tmpDir, g)
	if err != nil {
		t.Fatalf("write graph failed: %v", err)
	}

	// Check graph.json exists.
	graphPath := filepath.Join(outputDir, "graph.json")
	if _, err := os.Stat(graphPath); os.IsNotExist(err) {
		t.Error("expected graph.json to exist")
	}

	// Check manifest.json exists.
	manifestPath := filepath.Join(outputDir, "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Error("expected manifest.json to exist")
	}

	// Check identities directory exists.
	identDir := filepath.Join(outputDir, "identities")
	entries, err := os.ReadDir(identDir)
	if err != nil {
		t.Fatalf("failed to read identities dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 identity files, got %d", len(entries))
	}
}
