package astindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunnerBuildsSummary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (Runner{}).Run(context.Background(), Input{SnapshotPath: root, PrimaryLanguage: "go", Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	if out.Index == nil || out.Summary.Files != 1 {
		t.Fatalf("output = %+v", out.Summary)
	}
}
