package parser

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseStructuredYAML(t *testing.T) {
	content := []byte("service:\n  name: api\n")
	got, ok, err := parseStructured("config.yaml", content)
	if err != nil {
		t.Fatalf("parseStructured error: %v", err)
	}
	if !ok {
		t.Fatalf("expected structured parser to match")
	}
	if got["format"] != "yaml" {
		t.Fatalf("expected yaml format, got %#v", got["format"])
	}
}

func TestParseFileTreeSitterGo(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc hello() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	artifact, err := parseFile(context.Background(), "snap", file, "main.go", "hash")
	if err != nil {
		t.Fatalf("parseFile error: %v", err)
	}
	if artifact.ArtifactType != "tree_sitter" {
		t.Fatalf("expected tree_sitter artifact, got %q", artifact.ArtifactType)
	}
	if artifact.Language != "go" {
		t.Fatalf("expected go language, got %q", artifact.Language)
	}
	if len(artifact.Symbols) == 0 {
		t.Fatalf("expected symbols extracted")
	}
}

func TestRunWritesReport(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap1"}); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "parse", "report.json")); err != nil {
		t.Fatalf("expected report file: %v", err)
	}
}
