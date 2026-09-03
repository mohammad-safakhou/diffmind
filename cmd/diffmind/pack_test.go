package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackCLIEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DIFFMIND_HOME", home)
	packDir := filepath.Join(t.TempDir(), "company-conventions")
	var stdout, stderr bytes.Buffer

	if err := runPack([]string{"init", packDir, "--id", "company.conventions"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(packDir, "pack.yaml")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runPack([]string{"lint", packDir}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "company.conventions@0.1.0") {
		t.Fatalf("lint: output=%q err=%v", stdout.String(), err)
	}
	stdout.Reset()
	if err := runPack([]string{"test", packDir}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "extracts service name") {
		t.Fatalf("test: output=%q err=%v", stdout.String(), err)
	}
	if !strings.Contains(stdout.String(), "resolves a declared client") {
		t.Fatalf("scaffold graph test was not executed: %s", stdout.String())
	}
	stdout.Reset()
	fixture := filepath.Join(packDir, "testdata", "basic")
	if err := runPack([]string{"explain", filepath.Join(packDir, "pack.yaml"), "--repo", fixture}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"service_name": "example-service"`) {
		t.Fatalf("explain output: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"pack_id": "company.conventions"`) || !strings.Contains(stdout.String(), `"start_line": 3`) {
		t.Fatalf("explain lost detector evidence: %s", stdout.String())
	}
	stdout.Reset()
	if err := runPack([]string{"install", packDir}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "sha256:") {
		t.Fatalf("install output: %s", stdout.String())
	}
	stdout.Reset()
	if err := runPack([]string{"list"}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "company.conventions") {
		t.Fatalf("list: output=%q err=%v", stdout.String(), err)
	}
	if err := runPack([]string{"disable", "company.conventions"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := runPack([]string{"enable", "company.conventions"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestPackCLIRejectsUntestedPack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.yaml"), []byte(`api_version: diffmind.dev/v1alpha1
kind: KnowledgePack
id: no-tests
name: No tests
version: 1.0.0
license: Apache-2.0
compatibility: ">=0.1.0"
applies_to:
  kind: service_repo
extractions:
  - name: identity
    source: {glob: service.yaml}
    extract:
      - {field: name, maps_to: service_name}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runPack([]string{"test", dir}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no-tests") {
		t.Fatalf("expected no-tests failure, got %v", err)
	}
}
