package classifier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReportServiceRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "go.mod", "module x\n")
	mustWrite(t, root, "cmd/api/main.go", "package main\n")
	mustWrite(t, root, "Dockerfile", "FROM scratch\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "name: ci\n")
	mustWrite(t, root, "openapi.yaml", "openapi: 3.0.0\n")

	files, stats, err := ScanTree(root)
	if err != nil {
		t.Fatalf("ScanTree failed: %v", err)
	}
	report := BuildReport(root, files, stats)

	if !hasCapability(report.Capabilities.BuildTools, "go-mod") {
		t.Fatalf("expected go-mod capability")
	}
	if !hasCapability(report.Capabilities.CI, "github-actions") {
		t.Fatalf("expected github-actions capability")
	}
	if !hasCapability(report.Capabilities.Containers, "dockerfile") {
		t.Fatalf("expected dockerfile capability")
	}
	if !hasLabel(report.Profile.Labels, "service-repo") {
		t.Fatalf("expected service-repo label, got %+v", report.Profile.Labels)
	}
	assertCapabilityEvidence(t, report.Capabilities)
}

func TestBuildReportInfraRepo(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, root, "infra/main.tf", "resource \"x\" \"y\" {}\n")
	mustWrite(t, root, "helm/Chart.yaml", "apiVersion: v2\n")
	mustWrite(t, root, "k8s/deploy.yaml", "apiVersion: apps/v1\n")
	mustWrite(t, root, "README.md", "infra\n")

	files, stats, err := ScanTree(root)
	if err != nil {
		t.Fatalf("ScanTree failed: %v", err)
	}
	report := BuildReport(root, files, stats)

	if !hasCapability(report.Capabilities.IaC, "terraform") {
		t.Fatalf("expected terraform capability")
	}
	if !hasCapability(report.Capabilities.IaC, "helm") {
		t.Fatalf("expected helm capability")
	}
	if !hasLabel(report.Profile.Labels, "infra-repo") {
		t.Fatalf("expected infra-repo label, got %+v", report.Profile.Labels)
	}
	assertCapabilityEvidence(t, report.Capabilities)
}

func assertCapabilityEvidence(t *testing.T, caps RepoCapabilities) {
	t.Helper()
	all := [][]Capability{caps.BuildTools, caps.CI, caps.IaC, caps.APISpecs, caps.Migrations, caps.Containers}
	for _, group := range all {
		for _, c := range group {
			if len(c.Evidence) == 0 {
				t.Fatalf("capability %q has no evidence", c.Name)
			}
		}
	}
}

func hasCapability(caps []Capability, name string) bool {
	for _, c := range caps {
		if c.Name == name {
			return true
		}
	}
	return false
}

func hasLabel(labels []LabelScore, name string) bool {
	for _, l := range labels {
		if l.Label == name {
			return true
		}
	}
	return false
}

func mustWrite(t *testing.T, root string, rel string, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
