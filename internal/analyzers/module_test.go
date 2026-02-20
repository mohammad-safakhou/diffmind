package analyzers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"diffmind/internal/facts"
)

func TestAnalyzeProducesCoreFactTypes(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nimport \"net/http\"\nfunc main(){http.Get(\"https://example.com\")}\n")
	mustWrite(t, root, "cmd/queue.go", "package main\nfunc q(){ writer.WriteMessages(ctx, kafka.Message{Topic:\"orders.events\"}); _,_ = ch.Consume(\"orders.events\", \"\", false, false, false, false, nil)}\n")
	mustWrite(t, root, "cmd/db.go", "package main\nimport \"database/sql\"\nfunc db(){ db, _ := sql.Open(\"postgres\", dsn); _ = db.Query(\"select 1\"); _ = db.Exec(\"insert into t values (1)\") }\n")
	mustWrite(t, root, "internal/http/routes.js", "app.get('/health', handler)\nfetch('https://api.example.com')\n")
	mustWrite(t, root, "internal/config/app.go", "package config\nimport \"os\"\nfunc load(){ _ = os.Getenv(\"APP_ENV\") }\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "jobs:\n  build:\n    steps:\n      - uses: actions/checkout@v4\n      - run: go test ./...\n")
	mustWrite(t, root, "infra/main.tf", "resource \"aws_s3_bucket\" \"state\" {}\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-analyzer"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	bundlePath := filepath.Join(out, "analyzers", "bundle.json")
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}

	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if err := facts.ValidateBundle(bundle); err != nil {
		t.Fatalf("bundle should validate: %v", err)
	}

	assertFactType(t, bundle, "RuntimeUnit")
	assertFactType(t, bundle, "Endpoint")
	assertFactType(t, bundle, "ExternalCall")
	assertFactType(t, bundle, "ConfigKey")
	assertFactType(t, bundle, "PipelineStep")
	assertFactType(t, bundle, "InfraResource")
	assertExternalCallProtocol(t, bundle, "queue")
	assertExternalCallProtocol(t, bundle, "db")
}

func TestRunWithExtractorSelection(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "cmd/main.go", "package main\nimport \"net/http\"\nfunc main(){http.Get(\"https://example.com\")}\n")
	mustWrite(t, root, "internal/http/routes.js", "app.get('/health', handler)\n")
	mustWrite(t, root, "internal/config/app.go", "package config\nimport \"os\"\nfunc load(){ _ = os.Getenv(\"APP_ENV\") }\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-analyzer", "--extractors", "runtime,config"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	assertFactType(t, bundle, "RuntimeUnit")
	assertFactType(t, bundle, "ConfigKey")
	assertNoFactType(t, bundle, "Endpoint")
	assertNoFactType(t, bundle, "ExternalCall")

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Extractors) != 2 || report.Extractors[0] != "config" || report.Extractors[1] != "runtime" {
		t.Fatalf("unexpected report extractors: %#v", report.Extractors)
	}
}

func TestRunRejectsUnknownExtractor(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	err := Run(context.Background(), []string{"--source", root, "--out", out, "--extractors", "runtime,unknown"})
	if err == nil {
		t.Fatalf("expected error for unknown extractor")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, `unsupported extractor "unknown"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunRejectsUnknownAdapter(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	err := Run(context.Background(), []string{"--source", root, "--out", out, "--adapters", "builtin,missing"})
	if err == nil {
		t.Fatalf("expected error for unknown adapter")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, `unsupported adapter "missing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunFailsWhenSelectedAdapterUnavailableByDefault(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")
	t.Setenv("DIFFMIND_GOPLS_BIN", filepath.Join(t.TempDir(), "missing-gopls"))

	err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--adapters", "gopls",
		"--extractors", "runtime",
	})
	if err == nil {
		t.Fatalf("expected unavailable adapter error")
	}
	if got := err.Error(); !strings.Contains(got, `adapter "gopls" unavailable`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunAllowsMissingSelectedAdapterWhenFlagEnabled(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")
	t.Setenv("DIFFMIND_GOPLS_BIN", filepath.Join(t.TempDir(), "missing-gopls"))

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-missing-gopls-ok",
		"--adapters", "gopls",
		"--allow-missing-adapters",
		"--extractors", "runtime",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.AdapterPlan) != 1 || report.AdapterPlan[0].Name != "gopls" {
		t.Fatalf("unexpected adapter plan: %#v", report.AdapterPlan)
	}
	if report.AdapterPlan[0].Available {
		t.Fatalf("expected gopls to be unavailable in plan")
	}
	if len(report.AdapterRuns) != 0 {
		t.Fatalf("expected no adapter runs when adapter unavailable, got: %#v", report.AdapterRuns)
	}
}

func TestRunWithAdapterSelectionWritesPlanAndRuns(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-adapter",
		"--adapters", "builtin",
		"--extractors", "runtime",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Adapters) != 1 || report.Adapters[0] != "builtin" {
		t.Fatalf("unexpected adapters: %#v", report.Adapters)
	}
	if len(report.AdapterPlan) != 1 || report.AdapterPlan[0].Name != "builtin" || !report.AdapterPlan[0].Available {
		t.Fatalf("unexpected adapter plan: %#v", report.AdapterPlan)
	}
	if strings.TrimSpace(report.AdapterPlan[0].ToolchainSHA) == "" {
		t.Fatalf("expected adapter plan toolchain sha")
	}
	if len(report.AdapterRuns) != 1 || report.AdapterRuns[0].Name != "builtin" {
		t.Fatalf("unexpected adapter runs: %#v", report.AdapterRuns)
	}
	if report.AdapterRuns[0].ReplayKey == "" {
		t.Fatalf("expected replay_key in adapter run metadata")
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolchainSHA) == "" {
		t.Fatalf("expected adapter run toolchain sha")
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolOutputPath) != "" || strings.TrimSpace(report.AdapterRuns[0].ToolOutputSHA256) != "" {
		t.Fatalf("expected no tool output artifact for builtin adapter")
	}
	if strings.TrimSpace(report.AdapterRuns[0].RunManifestPath) == "" || strings.TrimSpace(report.AdapterRuns[0].RunManifestSHA256) == "" {
		t.Fatalf("expected adapter run manifest metadata")
	}
	if _, err := os.Stat(report.AdapterRuns[0].RunManifestPath); err != nil {
		t.Fatalf("expected adapter run manifest file: %v", err)
	}
	if len(report.AdapterRuns[0].Extractors) != 1 || report.AdapterRuns[0].Extractors[0] != "runtime" {
		t.Fatalf("unexpected run extractors: %#v", report.AdapterRuns[0].Extractors)
	}
	if !report.Offline {
		t.Fatalf("expected offline=true by default")
	}
	if report.ToolchainManifestPath == "" || report.ToolchainManifestSHA256 == "" {
		t.Fatalf("expected toolchain manifest metadata in report")
	}
	if _, err := os.Stat(report.ToolchainManifestPath); err != nil {
		t.Fatalf("expected toolchain manifest file: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !allFactsHaveAdapterAttr(bundle, "builtin", "v1", report.AdapterRuns[0].ToolchainSHA) {
		t.Fatalf("expected builtin adapter provenance attributes on all facts")
	}
}

func TestRunWithGoplsAdapterSelectionWritesPlanAndRuns(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "gopls")
	mustWrite(t, binDir, "gopls", "#!/bin/sh\nif [ \"$1\" = \"version\" ]; then\n  echo \"gopls test-version\"\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod gopls stub: %v", err)
	}
	t.Setenv("DIFFMIND_GOPLS_BIN", bin)

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-gopls",
		"--adapters", "gopls",
		"--extractors", "runtime",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Adapters) != 1 || report.Adapters[0] != "gopls" {
		t.Fatalf("unexpected adapters: %#v", report.Adapters)
	}
	if len(report.AdapterPlan) != 1 || report.AdapterPlan[0].Name != "gopls" || !report.AdapterPlan[0].Available {
		t.Fatalf("unexpected adapter plan: %#v", report.AdapterPlan)
	}
	if strings.TrimSpace(report.AdapterPlan[0].ToolPath) == "" || strings.TrimSpace(report.AdapterPlan[0].ToolVersion) == "" || strings.TrimSpace(report.AdapterPlan[0].ToolchainSHA) == "" {
		t.Fatalf("expected gopls tool metadata in plan: %#v", report.AdapterPlan[0])
	}
	if len(report.AdapterRuns) != 1 || report.AdapterRuns[0].Name != "gopls" {
		t.Fatalf("unexpected adapter runs: %#v", report.AdapterRuns)
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolPath) == "" || strings.TrimSpace(report.AdapterRuns[0].ToolVersion) == "" || strings.TrimSpace(report.AdapterRuns[0].ToolchainSHA) == "" {
		t.Fatalf("expected gopls tool metadata in run: %#v", report.AdapterRuns[0])
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolExecStatus) != "executed" {
		t.Fatalf("expected gopls tool exec status executed, got: %q", report.AdapterRuns[0].ToolExecStatus)
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolOutputPath) == "" || strings.TrimSpace(report.AdapterRuns[0].ToolOutputSHA256) == "" {
		t.Fatalf("expected gopls tool output artifact metadata")
	}
	if _, err := os.Stat(report.AdapterRuns[0].ToolOutputPath); err != nil {
		t.Fatalf("expected gopls tool output artifact file: %v", err)
	}
	if strings.TrimSpace(report.AdapterRuns[0].RunManifestPath) == "" || strings.TrimSpace(report.AdapterRuns[0].RunManifestSHA256) == "" {
		t.Fatalf("expected gopls run manifest metadata")
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !allFactsHaveAdapterAttr(bundle, "gopls", "v1", report.AdapterRuns[0].ToolchainSHA) {
		t.Fatalf("expected gopls adapter provenance attributes on all facts")
	}
}

func TestRunWithGoplsAdapterSemanticOutputMergesFacts(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "gopls")
	mustWrite(t, binDir, "gopls", "#!/bin/sh\nif [ \"$1\" = \"version\" ]; then\n  echo \"gopls test-version\"\n  exit 0\nfi\nif [ \"$1\" = \"semantic\" ]; then\n  cat <<'EOF'\n{\"facts\":[{\"type\":\"CodeSymbol\",\"attributes\":{\"name\":\"ToolMain\",\"language\":\"go\",\"kind\":\"function\"},\"evidence\":{\"file\":\"cmd/main.go\",\"line\":1,\"col\":1,\"snippet\":\"func main(){}\"}}]}\nEOF\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod gopls stub: %v", err)
	}
	t.Setenv("DIFFMIND_GOPLS_BIN", bin)
	t.Setenv("DIFFMIND_GOPLS_SEMANTIC_ARGS", "semantic")

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-gopls-semantic",
		"--adapters", "gopls",
		"--extractors", "runtime",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.AdapterRuns) != 1 {
		t.Fatalf("expected adapter run")
	}
	run := report.AdapterRuns[0]
	if run.ToolSemanticStatus != "executed" {
		t.Fatalf("expected semantic status executed, got %q", run.ToolSemanticStatus)
	}
	if strings.TrimSpace(run.ToolSemanticPath) == "" || strings.TrimSpace(run.ToolSemanticSHA256) == "" {
		t.Fatalf("expected semantic artifact metadata")
	}
	if run.ToolSemanticFactsAdded < 1 || run.ToolSemanticEvidenceAdded < 1 {
		t.Fatalf("expected semantic facts/evidence added, got facts=%d evidence=%d", run.ToolSemanticFactsAdded, run.ToolSemanticEvidenceAdded)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !hasFactTypeAndAttr(bundle, "CodeSymbol", "name", "ToolMain") {
		t.Fatalf("expected merged tool-native CodeSymbol fact")
	}
}

func TestRunWithGoplsAdapterSemanticStructuredOutputMergesFacts(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "go.mod", "module example.com/test\n")
	mustWrite(t, root, "cmd/main.go", "package main\nimport \"net/http\"\nfunc main(){http.Get(\"https://api.example.com\")}\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "gopls")
	mustWrite(t, binDir, "gopls", "#!/bin/sh\nif [ \"$1\" = \"version\" ]; then\n  echo \"gopls test-version\"\n  exit 0\nfi\nif [ \"$1\" = \"semantic-structured\" ]; then\n  cat <<'EOF'\n{\"symbols\":[{\"name\":\"ToolMain\",\"kind\":\"function\",\"file\":\"cmd/main.go\",\"line\":1,\"col\":1,\"snippet\":\"func main(){...}\"}],\"calls\":[{\"caller\":\"main\",\"callee\":\"http.Get\",\"kind\":\"function_call\",\"file\":\"cmd/main.go\",\"line\":1,\"col\":20,\"snippet\":\"http.Get(\\\"https://api.example.com\\\")\"}],\"external_calls\":[{\"protocol\":\"http\",\"method\":\"GET\",\"target\":\"api.example.com\",\"file\":\"cmd/main.go\",\"line\":1,\"col\":20,\"snippet\":\"http.Get(\\\"https://api.example.com\\\")\"}]}\nEOF\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod gopls stub: %v", err)
	}
	t.Setenv("DIFFMIND_GOPLS_BIN", bin)
	t.Setenv("DIFFMIND_GOPLS_SEMANTIC_ARGS", "semantic-structured")

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-gopls-semantic-structured",
		"--adapters", "gopls",
		"--extractors", "runtime",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.AdapterRuns) != 1 {
		t.Fatalf("expected adapter run")
	}
	run := report.AdapterRuns[0]
	if run.ToolSemanticStatus != "executed" {
		t.Fatalf("expected semantic status executed, got %q", run.ToolSemanticStatus)
	}
	if run.ToolSemanticFactsAdded < 3 {
		t.Fatalf("expected structured semantic facts merged, got facts=%d", run.ToolSemanticFactsAdded)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !hasFactTypeAndAttr(bundle, "CodeSymbol", "name", "ToolMain") {
		t.Fatalf("expected structured semantic CodeSymbol fact")
	}
	if !hasFactTypeAndAttr(bundle, "CodeCall", "callee", "http.Get") {
		t.Fatalf("expected structured semantic CodeCall fact")
	}
	if !hasFactTypeAndAttr(bundle, "ExternalCall", "target", "api.example.com") {
		t.Fatalf("expected structured semantic ExternalCall fact")
	}
}

func TestRunWithTsserverAdapterSelectionWritesPlanAndRuns(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "src/app.ts", "export function ping(){ return 'ok'; }\nping()\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "tsserver")
	mustWrite(t, binDir, "tsserver", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"tsserver test-version\"\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod tsserver stub: %v", err)
	}
	t.Setenv("DIFFMIND_TSSERVER_BIN", bin)

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-tsserver",
		"--adapters", "tsserver",
		"--extractors", "semantic_model",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Adapters) != 1 || report.Adapters[0] != "tsserver" {
		t.Fatalf("unexpected adapters: %#v", report.Adapters)
	}
	if len(report.AdapterPlan) != 1 || report.AdapterPlan[0].Name != "tsserver" || !report.AdapterPlan[0].Available {
		t.Fatalf("unexpected adapter plan: %#v", report.AdapterPlan)
	}
	if len(report.AdapterRuns) != 1 || report.AdapterRuns[0].Name != "tsserver" {
		t.Fatalf("unexpected adapter runs: %#v", report.AdapterRuns)
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolchainSHA) == "" || strings.TrimSpace(report.AdapterRuns[0].RunManifestPath) == "" {
		t.Fatalf("expected tsserver run metadata: %#v", report.AdapterRuns[0])
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolExecStatus) != "executed" {
		t.Fatalf("expected tsserver tool exec status executed, got: %q", report.AdapterRuns[0].ToolExecStatus)
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolOutputPath) == "" || strings.TrimSpace(report.AdapterRuns[0].ToolOutputSHA256) == "" {
		t.Fatalf("expected tsserver tool output artifact metadata")
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if len(bundle.Facts) == 0 {
		t.Fatalf("expected non-empty fact set for tsserver adapter run")
	}
	if !allFactsHaveAdapterAttr(bundle, "tsserver", "v1", report.AdapterRuns[0].ToolchainSHA) {
		t.Fatalf("expected tsserver adapter provenance attributes on all facts")
	}
}

func TestRunWithPyrightAdapterSelectionWritesPlanAndRuns(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "app.py", "def ping_py():\n    return None\n\nping_py()\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "pyright-langserver")
	mustWrite(t, binDir, "pyright-langserver", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"pyright test-version\"\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod pyright stub: %v", err)
	}
	t.Setenv("DIFFMIND_PYRIGHT_BIN", bin)

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-pyright",
		"--adapters", "pyright",
		"--extractors", "semantic_model",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(report.Adapters) != 1 || report.Adapters[0] != "pyright" {
		t.Fatalf("unexpected adapters: %#v", report.Adapters)
	}
	if len(report.AdapterPlan) != 1 || report.AdapterPlan[0].Name != "pyright" || !report.AdapterPlan[0].Available {
		t.Fatalf("unexpected adapter plan: %#v", report.AdapterPlan)
	}
	if len(report.AdapterRuns) != 1 || report.AdapterRuns[0].Name != "pyright" {
		t.Fatalf("unexpected adapter runs: %#v", report.AdapterRuns)
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolchainSHA) == "" || strings.TrimSpace(report.AdapterRuns[0].RunManifestPath) == "" {
		t.Fatalf("expected pyright run metadata: %#v", report.AdapterRuns[0])
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolExecStatus) != "executed" {
		t.Fatalf("expected pyright tool exec status executed, got: %q", report.AdapterRuns[0].ToolExecStatus)
	}
	if strings.TrimSpace(report.AdapterRuns[0].ToolOutputPath) == "" || strings.TrimSpace(report.AdapterRuns[0].ToolOutputSHA256) == "" {
		t.Fatalf("expected pyright tool output artifact metadata")
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if len(bundle.Facts) == 0 {
		t.Fatalf("expected non-empty fact set for pyright adapter run")
	}
	if !allFactsHaveAdapterAttr(bundle, "pyright", "v1", report.AdapterRuns[0].ToolchainSHA) {
		t.Fatalf("expected pyright adapter provenance attributes on all facts")
	}
}

func TestRunWithTsserverAdapterSemanticStructuredOutputMergesFacts(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "src/app.ts", "import axios from 'axios'; export function ping(){ return axios.get('https://api.example.com'); }\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "tsserver")
	mustWrite(t, binDir, "tsserver", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"tsserver test-version\"\n  exit 0\nfi\nif [ \"$1\" = \"semantic-structured\" ]; then\n  cat <<'EOF'\n{\"packages\":[{\"name\":\"app\",\"files\":[{\"path\":\"src/app.ts\",\"symbols\":[{\"name\":\"ping\",\"kind\":\"function\",\"line\":1,\"col\":1,\"snippet\":\"function ping(){...}\"}],\"calls\":[{\"caller\":\"ping\",\"callee\":\"axios.get\",\"kind\":\"method_call\",\"line\":1,\"col\":40,\"snippet\":\"axios.get(...)\"}],\"external_calls\":[{\"protocol\":\"http\",\"method\":\"GET\",\"target\":\"api.example.com\",\"line\":1,\"col\":40,\"snippet\":\"axios.get(...)\"}],\"imports\":[{\"module\":\"axios\",\"kind\":\"npm\",\"line\":1,\"col\":1,\"snippet\":\"import axios from 'axios'\"}]}]}]}\nEOF\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod tsserver stub: %v", err)
	}
	t.Setenv("DIFFMIND_TSSERVER_BIN", bin)
	t.Setenv("DIFFMIND_TSSERVER_SEMANTIC_ARGS", "semantic-structured")

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-tsserver-semantic-structured",
		"--adapters", "tsserver",
		"--extractors", "semantic_model",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !hasFactTypeAndAttr(bundle, "CodeSymbol", "name", "ping") {
		t.Fatalf("expected structured semantic tsserver CodeSymbol fact")
	}
	if !hasFactTypeAndAttr(bundle, "CodeCall", "callee", "axios.get") {
		t.Fatalf("expected structured semantic tsserver CodeCall fact")
	}
	if !hasFactTypeAndAttr(bundle, "ExternalCall", "target", "api.example.com") {
		t.Fatalf("expected structured semantic tsserver ExternalCall fact")
	}
	if !hasFactTypeAndAttr(bundle, "Dependency", "module", "axios") {
		t.Fatalf("expected structured semantic tsserver Dependency fact")
	}
}

func TestRunWithPyrightAdapterSemanticStructuredOutputMergesFacts(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "app.py", "import requests\n\ndef ping_py():\n    return requests.get('https://api.example.com')\n")

	binDir := t.TempDir()
	bin := filepath.Join(binDir, "pyright-langserver")
	mustWrite(t, binDir, "pyright-langserver", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"pyright test-version\"\n  exit 0\nfi\nif [ \"$1\" = \"semantic-structured\" ]; then\n  cat <<'EOF'\n{\"packages\":[{\"name\":\"app\",\"files\":[{\"path\":\"app.py\",\"symbols\":[{\"name\":\"ping_py\",\"kind\":\"function\",\"line\":3,\"col\":1,\"snippet\":\"def ping_py():\"}],\"calls\":[{\"caller\":\"ping_py\",\"callee\":\"requests.get\",\"kind\":\"function_call\",\"line\":4,\"col\":12,\"snippet\":\"requests.get(...)\"}],\"external_calls\":[{\"protocol\":\"http\",\"method\":\"GET\",\"target\":\"api.example.com\",\"line\":4,\"col\":12,\"snippet\":\"requests.get(...)\"}],\"dependencies\":[{\"module\":\"requests\",\"kind\":\"pip\",\"line\":1,\"col\":1,\"snippet\":\"import requests\"}]}]}]}\nEOF\n  exit 0\nfi\nexit 1\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatalf("chmod pyright stub: %v", err)
	}
	t.Setenv("DIFFMIND_PYRIGHT_BIN", bin)
	t.Setenv("DIFFMIND_PYRIGHT_SEMANTIC_ARGS", "semantic-structured")

	if err := Run(context.Background(), []string{
		"--source", root,
		"--out", out,
		"--snapshot-id", "snap-pyright-semantic-structured",
		"--adapters", "pyright",
		"--extractors", "semantic_model",
	}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !hasFactTypeAndAttr(bundle, "CodeSymbol", "name", "ping_py") {
		t.Fatalf("expected structured semantic pyright CodeSymbol fact")
	}
	if !hasFactTypeAndAttr(bundle, "CodeCall", "callee", "requests.get") {
		t.Fatalf("expected structured semantic pyright CodeCall fact")
	}
	if !hasFactTypeAndAttr(bundle, "ExternalCall", "target", "api.example.com") {
		t.Fatalf("expected structured semantic pyright ExternalCall fact")
	}
	if !hasFactTypeAndAttr(bundle, "Dependency", "module", "requests") {
		t.Fatalf("expected structured semantic pyright Dependency fact")
	}
}

func TestRunOfflineRejectsLLMAugment(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")
	mustWrite(t, root, "cmd/main.go", "package main\nfunc main(){}\n")

	err := Run(context.Background(), []string{"--source", root, "--out", out, "--llm-augment"})
	if err == nil {
		t.Fatalf("expected error for llm augment in offline mode")
	}
	if got := err.Error(); !strings.Contains(got, "disabled in offline mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func allFactsHaveAdapterAttr(bundle facts.Bundle, adapterName string, adapterVersion string, toolchainSHA string) bool {
	for _, f := range bundle.Facts {
		gotName, _ := f.Attributes["adapter_id"].(string)
		gotVersion, _ := f.Attributes["adapter_version"].(string)
		gotToolchainSHA, _ := f.Attributes["toolchain_sha"].(string)
		if strings.TrimSpace(gotName) != adapterName || strings.TrimSpace(gotVersion) != adapterVersion || strings.TrimSpace(gotToolchainSHA) != strings.TrimSpace(toolchainSHA) {
			return false
		}
	}
	return true
}

func TestGoSemanticExtractionHandlesAliasedNetHTTP(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "cmd/main.go", `
package main

import nethttp "net/http"

func main() {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("/healthz", func(w nethttp.ResponseWriter, r *nethttp.Request) {})
	endpoint := "https://api.example.com/users"
	req, _ := nethttp.NewRequest(nethttp.MethodPut, endpoint, nil)
	_ = req
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "ANY", "/healthz") {
		t.Fatalf("expected semantic endpoint for HandleFunc")
	}
	if !hasExternalCall(bundle, "PUT", "endpoint") {
		t.Fatalf("expected semantic outbound call for aliased net/http NewRequest")
	}
}

func TestGoSemanticExtractionMuxMethodsAndDependencyCalls(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "cmd/main.go", `
package main

import (
	"context"
	"database/sql"
	"os/exec"
)

func run(ctx context.Context, r any, writer any, ch any) {
	r.HandleFunc("/users", func(){}).Methods("GET", "POST")
	writer.WriteMessages(ctx, struct{Topic string}{Topic: "orders.events"})
	_, _ = ch.Consume("orders.events", "", false, false, false, false, nil)
	db, _ := sql.Open("postgres", "dsn")
	_, _ = db.Query("SELECT * FROM users")
	_, _ = db.Exec("UPDATE users SET active=true")
	_, _ = exec.Command("echo", "done")
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-go-semantic-advanced", "--extractors", "endpoint,queue_db"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/users") || !hasEndpoint(bundle, "POST", "/users") {
		t.Fatalf("expected semantic gorilla mux endpoint methods")
	}
	if !hasExternalCall(bundle, "PUBLISH", "orders.events") {
		t.Fatalf("expected semantic Go queue publish call")
	}
	if !hasExternalCall(bundle, "CONSUME", "orders.events") {
		t.Fatalf("expected semantic Go queue consume call")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "db", "CONNECT") {
		t.Fatalf("expected semantic Go DB connect call")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "db", "READ") || !hasExternalCallWithProtocolAndMethod(bundle, "db", "WRITE") {
		t.Fatalf("expected semantic Go DB read/write calls")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "command", "EXEC") {
		t.Fatalf("expected semantic Go command execution call")
	}
}

func TestJSTSSemanticExtractionRouterAndAxiosClient(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "internal/http/routes.ts", `
import express from "express";
import axios from "axios";

const api = express.Router();
api.get("/healthz", (req, res) => res.send("ok"));

const client = axios.create();
client.post("https://svc.local/orders", { id: 1 });
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-jsts-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/healthz") {
		t.Fatalf("expected semantic endpoint for express router variable")
	}
	if !hasExternalCall(bundle, "POST", "https://svc.local/orders") {
		t.Fatalf("expected semantic outbound call for axios client variable")
	}
}

func TestJSTSSemanticExtractionNestMountedRouterAndHTTPMethods(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "internal/http/routes.ts", `
import express from "express";

const app = express();
const api = express.Router();
api.get("/orders", (_req, res) => res.send("ok"));
app.use("/v1", api);
`)
	mustWrite(t, root, "src/orders.controller.ts", `
import { Controller, Get } from "@nestjs/common";

@Controller("/orders")
export class OrdersController {
  @Get("/health")
  health() {
    return "ok";
  }
}
`)
	mustWrite(t, root, "internal/http/client.ts", `
import axios from "axios";

fetch("https://svc.local/items", { method: "PATCH" });
axios({ url: "https://svc.local/delete", method: "DELETE" });
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-jsts-semantic-advanced", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/v1/orders") {
		t.Fatalf("expected semantic endpoint for mounted express router")
	}
	if !hasEndpoint(bundle, "GET", "/orders/health") {
		t.Fatalf("expected semantic endpoint for NestJS controller decorator")
	}
	if !hasExternalCall(bundle, "PATCH", "https://svc.local/items") {
		t.Fatalf("expected semantic outbound call with fetch method parsing")
	}
	if !hasExternalCall(bundle, "DELETE", "https://svc.local/delete") {
		t.Fatalf("expected semantic outbound call for axios({method,url})")
	}
}

func TestJSTSSemanticDependencyExtractionQueueDBAndCommand(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "internal/deps.ts", `
async function runAll(producer: any, consumer: any, channel: any, sqs: any, prisma: any, child: any) {
  await producer.send({ topic: "orders.events", messages: [{ value: "x" }] });
  await consumer.subscribe({ topic: "orders.events" });
  channel.sendToQueue("billing.queue", Buffer.from("x"));
  channel.consume("billing.queue", () => {});
  sqs.sendMessage({ QueueUrl: "https://sqs.aws.local/orders" });
  sqs.receiveMessage({ QueueUrl: "https://sqs.aws.local/orders" });
  await prisma.order.findMany();
  await prisma.order.update({ where: { id: 1 }, data: { status: "done" } });
  child.exec("echo done");
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-jsts-deps", "--extractors", "queue_db"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasExternalCall(bundle, "PUBLISH", "orders.events") {
		t.Fatalf("expected semantic Kafka publish dependency")
	}
	if !hasExternalCall(bundle, "CONSUME", "orders.events") {
		t.Fatalf("expected semantic Kafka consume dependency")
	}
	if !hasExternalCall(bundle, "PUBLISH", "billing.queue") || !hasExternalCall(bundle, "CONSUME", "billing.queue") {
		t.Fatalf("expected semantic RabbitMQ publish/consume dependencies")
	}
	if !hasExternalCall(bundle, "PUBLISH", "https://sqs.aws.local/orders") || !hasExternalCall(bundle, "CONSUME", "https://sqs.aws.local/orders") {
		t.Fatalf("expected semantic SQS publish/consume dependencies")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "db", "READ") || !hasExternalCallWithProtocolAndMethod(bundle, "db", "WRITE") {
		t.Fatalf("expected semantic Prisma read/write dependencies")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "command", "EXEC") {
		t.Fatalf("expected semantic command execution dependency")
	}
}

func TestPythonSemanticExtractionFlaskRouteAndRequests(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "app.py", `
from flask import Flask
import requests as rq

app = Flask(__name__)

@app.route("/health", methods=["GET"])
def health():
    return "ok"

rq.post("https://svc.local/orders", json={"id": 1})
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-py-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/health") {
		t.Fatalf("expected semantic endpoint from @app.route")
	}
	if !hasExternalCall(bundle, "POST", "https://svc.local/orders") {
		t.Fatalf("expected semantic outbound call from requests alias")
	}
}

func TestPythonSemanticExtractionFastAPIAndHTTPX(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "app.py", `
from fastapi import FastAPI, APIRouter
import httpx

app = FastAPI()
router = APIRouter(prefix="/v1")

@router.get("/users")
def users():
    return []

app.include_router(router, prefix="/api")
httpx.request(method="PATCH", url="https://svc.local/items")
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-py-semantic-advanced", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/api/v1/users") {
		t.Fatalf("expected semantic endpoint from FastAPI router with include_router prefix")
	}
	if !hasExternalCall(bundle, "PATCH", "https://svc.local/items") {
		t.Fatalf("expected semantic outbound call from httpx.request")
	}
}

func TestPythonSemanticDependencyExtractionQueueDBAndCommand(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "deps.py", `
import subprocess
import os

def run_all(producer, consumer, sqs, session, celery_app):
    producer.send("orders.events", b"x")
    consumer.subscribe("orders.events")
    sqs.send_message(QueueUrl="https://sqs.aws.local/orders")
    sqs.receive_message(QueueUrl="https://sqs.aws.local/orders")
    session.execute("SELECT * FROM orders")
    session.execute("UPDATE orders SET status='done'")
    celery_app.send_task("billing.reconcile")
    subprocess.run("echo done", shell=True)
    os.system("echo done")
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-py-deps", "--extractors", "queue_db"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasExternalCall(bundle, "PUBLISH", "orders.events") {
		t.Fatalf("expected semantic Python queue publish call")
	}
	if !hasExternalCall(bundle, "CONSUME", "orders.events") {
		t.Fatalf("expected semantic Python queue consume call")
	}
	if !hasExternalCall(bundle, "PUBLISH", "https://sqs.aws.local/orders") ||
		!hasExternalCall(bundle, "CONSUME", "https://sqs.aws.local/orders") {
		t.Fatalf("expected semantic Python SQS publish/consume calls")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "db", "READ") || !hasExternalCallWithProtocolAndMethod(bundle, "db", "WRITE") {
		t.Fatalf("expected semantic Python DB read/write calls")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "command", "EXEC") {
		t.Fatalf("expected semantic Python command execution call")
	}
}

func TestJavaSemanticExtractionSpringAndRestTemplate(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/java/com/acme/Controller.java", `
package com.acme;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestMethod;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.web.client.RestTemplate;

@RestController
public class Controller {
  private final RestTemplate restTemplate = new RestTemplate();

  @GetMapping("/users")
  public String users() {
    return "ok";
  }

  @RequestMapping(value = "/orders", method = RequestMethod.POST)
  public String orders() {
    return restTemplate.postForObject("https://svc.local/orders", null, String.class);
  }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-java-semantic", "--extractors", "endpoint,external_http"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/users") {
		t.Fatalf("expected semantic endpoint from @GetMapping")
	}
	if !hasEndpoint(bundle, "POST", "/orders") {
		t.Fatalf("expected semantic endpoint from @RequestMapping(method=POST)")
	}
	if !hasExternalCall(bundle, "POST", "https://svc.local/orders") {
		t.Fatalf("expected semantic outbound call from RestTemplate.postForObject")
	}
}

func TestJavaSemanticExtractionClassMappingScheduleAndQueueConsumer(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/java/com/acme/Controller.java", `
package com.acme;

import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;
import org.springframework.kafka.annotation.KafkaListener;

@RestController
@RequestMapping("/api/v1")
public class Controller {
  @GetMapping("/users")
  public String users() {
    return "ok";
  }
}

@Component
class Worker {
  @Scheduled(cron = "0 0 * * * *")
  public void tick() {}

  @KafkaListener(topics = "orders.events")
  public void onOrder(String payload) {}
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-java-semantic-exposure", "--extractors", "endpoint"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasEndpoint(bundle, "GET", "/api/v1/users") {
		t.Fatalf("expected composed semantic endpoint from class + method mapping")
	}
	if !hasEndpoint(bundle, "SCHEDULE", "0 0 * * * *") {
		t.Fatalf("expected scheduled semantic exposure endpoint")
	}
	foundConsume := false
	for _, f := range bundle.Facts {
		if f.Type != "ExternalCall" {
			continue
		}
		protocol, _ := f.Attributes["protocol"].(string)
		method, _ := f.Attributes["method"].(string)
		target, _ := f.Attributes["target"].(string)
		if protocol == "queue" && method == "CONSUME" && target == "orders.events" {
			foundConsume = true
			break
		}
	}
	if !foundConsume {
		t.Fatalf("expected queue consumer semantic fact from @KafkaListener")
	}
}

func TestJavaSemanticDependencyExtractionHTTPQueueDBAndCommand(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/java/com/acme/Deps.java", `
package com.acme;

import org.springframework.web.client.RestTemplate;
import org.springframework.kafka.core.KafkaTemplate;
import org.springframework.jdbc.core.JdbcTemplate;

public class Deps {
  private final RestTemplate restTemplate = new RestTemplate();
  private final KafkaTemplate<String, String> kafkaTemplate = null;
  private final JdbcTemplate jdbcTemplate = null;

  public void run() throws Exception {
    restTemplate.getForEntity("https://svc.local/orders", String.class);
    kafkaTemplate.send("orders.events", "payload");
    jdbcTemplate.queryForList("SELECT * FROM orders");
    jdbcTemplate.update("UPDATE orders SET status='done'");
    Runtime.getRuntime().exec("echo hello");
  }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-java-deps", "--extractors", "external_http,queue_db"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasExternalCall(bundle, "GET", "https://svc.local/orders") {
		t.Fatalf("expected Java semantic outbound HTTP call")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "queue", "PUBLISH") {
		t.Fatalf("expected Java semantic queue publish call")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "db", "READ") {
		t.Fatalf("expected Java semantic db read call")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "db", "WRITE") {
		t.Fatalf("expected Java semantic db write call")
	}
	if !hasExternalCallWithProtocolAndMethod(bundle, "command", "EXEC") {
		t.Fatalf("expected Java semantic command execution call")
	}
}

func TestJavaSemanticExtractionFeignAndQueueTypeGuards(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/java/com/acme/PublisherClient.java", `
package com.acme;

import org.springframework.cloud.openfeign.FeignClient;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;

@FeignClient(name = "publisher-service", url = "${publisher.base-url}", path = "/v1")
public interface PublisherClient {
  @GetMapping("/publishers/{id}")
  String getPublisher();

  @PostMapping("/publishers")
  String createPublisher();
}
`)

	mustWrite(t, root, "src/main/java/com/acme/Notifier.java", `
package com.acme;

public class Notifier {
  private final Object campaignSseEmitters = null;
  private final java.util.Map<String, String> values = new java.util.HashMap<>();
  public void run() {
    campaignSseEmitters.send("not-a-queue-call");
    values.put("market", "de");
  }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-java-feign", "--extractors", "external_http,queue_db"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasExternalCall(bundle, "GET", "${publisher.base-url}/v1/publishers/{id}") {
		t.Fatalf("expected feign GET dependency")
	}
	if !hasExternalCall(bundle, "POST", "${publisher.base-url}/v1/publishers") {
		t.Fatalf("expected feign POST dependency")
	}
	if hasExternalCallWithProtocolAndMethod(bundle, "queue", "PUBLISH") {
		t.Fatalf("expected no queue publish dependency from generic .send call")
	}
	if hasExternalCall(bundle, "PUT", "market") {
		t.Fatalf("expected no HTTP dependency from generic map.put call")
	}
}

func TestJavaSemanticQueueRequestBuilderValueBinding(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/java/com/acme/Publisher.java", `
package com.acme;

import org.springframework.beans.factory.annotation.Value;
import software.amazon.awssdk.services.sqs.SqsClient;
import software.amazon.awssdk.services.sqs.model.SendMessageRequest;

public class Publisher {
  private final SqsClient sqsClient;
  @Value("${queue.name}")
  private String queueUrl;

  public Publisher(SqsClient sqsClient) {
    this.sqsClient = sqsClient;
  }

  public void send() {
    SendMessageRequest request = SendMessageRequest.builder()
      .queueUrl(queueUrl)
      .messageBody("x")
      .build();
    sqsClient.sendMessage(request);
  }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-java-sqs-bind", "--extractors", "queue_db"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasExternalCall(bundle, "PUBLISH", "cfg:queue.name") {
		t.Fatalf("expected queue target resolved via @Value binding")
	}
}

func TestRunExcludesTestSourcesByDefaultAndCanInclude(t *testing.T) {
	root := t.TempDir()
	outDefault := filepath.Join(root, ".diffmind-default")
	outInclude := filepath.Join(root, ".diffmind-include")

	mustWrite(t, root, "src/main/java/com/acme/MainController.java", `
package com.acme;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
@RestController
public class MainController {
  @GetMapping("/main")
  public String main() { return "ok"; }
}
`)

	mustWrite(t, root, "src/test/java/com/acme/TestController.java", `
package com.acme;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;
@RestController
public class TestController {
  @GetMapping("/from-test")
  public String test() { return "ok"; }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", outDefault, "--snapshot-id", "snap-no-tests", "--extractors", "endpoint"}); err != nil {
		t.Fatalf("Run default failed: %v", err)
	}
	if err := Run(context.Background(), []string{"--source", root, "--out", outInclude, "--snapshot-id", "snap-with-tests", "--extractors", "endpoint", "--include-tests"}); err != nil {
		t.Fatalf("Run include-tests failed: %v", err)
	}

	defaultData, err := os.ReadFile(filepath.Join(outDefault, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read default bundle: %v", err)
	}
	var defaultBundle facts.Bundle
	if err := json.Unmarshal(defaultData, &defaultBundle); err != nil {
		t.Fatalf("unmarshal default bundle: %v", err)
	}
	if !hasEndpoint(defaultBundle, "GET", "/main") {
		t.Fatalf("expected main endpoint in default mode")
	}
	if hasEndpoint(defaultBundle, "GET", "/from-test") {
		t.Fatalf("did not expect test endpoint in default mode")
	}

	includeData, err := os.ReadFile(filepath.Join(outInclude, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read include bundle: %v", err)
	}
	var includeBundle facts.Bundle
	if err := json.Unmarshal(includeData, &includeBundle); err != nil {
		t.Fatalf("unmarshal include bundle: %v", err)
	}
	if !hasEndpoint(includeBundle, "GET", "/from-test") {
		t.Fatalf("expected test endpoint when include-tests is enabled")
	}
}

func TestSemanticModelProducesUnifiedCodeFactsAcrossLanguages(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "go/main.go", `
package main
func ping() {}
func main(){ ping() }
`)
	mustWrite(t, root, "ts/app.ts", `
function pingTs() {}
pingTs()
`)
	mustWrite(t, root, "py/app.py", `
def ping_py():
    return None
ping_py()
`)
	mustWrite(t, root, "java/App.java", `
class App {
  void pingJava() {}
  void run() { pingJava(); }
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-unified-model", "--extractors", "semantic_model"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	assertFactType(t, bundle, "CodeSymbol")
	assertFactType(t, bundle, "CodeCall")

	for _, lang := range []string{"go", "typescript", "python", "java"} {
		if !hasFactWithLanguage(bundle, "CodeSymbol", lang) {
			t.Fatalf("expected CodeSymbol for language=%s", lang)
		}
		if !hasFactWithLanguage(bundle, "CodeCall", lang) {
			t.Fatalf("expected CodeCall for language=%s", lang)
		}
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.CodeSymbols == 0 || report.CodeCalls == 0 {
		t.Fatalf("expected non-zero semantic model counters in report: %+v", report)
	}
}

func TestM5RuntimeBuildDeployFactsExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "Dockerfile", "FROM golang:1.23\nRUN go build -o app ./cmd/app\n")
	mustWrite(t, root, "docker-compose.yml", "services:\n  api:\n    image: ghcr.io/acme/api:1.0\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "jobs:\n  build:\n    steps:\n      - run: docker build -t ghcr.io/acme/api:1.0 .\n")
	mustWrite(t, root, ".gitlab-ci.yml", "build-job:\n  stage: build\n  script:\n    - go build ./...\n    - docker build -t ghcr.io/acme/api:2.0 .\n")
	mustWrite(t, root, "Jenkinsfile", "pipeline {\n  stages {\n    stage('Build') { steps { sh 'go build ./...' } }\n    stage(\"Image\") { steps { sh \"docker build -t ghcr.io/acme/api:3.0 .\" } }\n  }\n}\n")
	mustWrite(t, root, "k8s/deployment.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    spec:\n      containers:\n      - name: api\n        image: ghcr.io/acme/api:1.0\n")
	mustWrite(t, root, "infra/main.tf", "resource \"aws_ecs_service\" \"api\" {}\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m5"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	assertFactType(t, bundle, "PipelineStep")
	assertFactType(t, bundle, "InfraResource")
	assertFactType(t, bundle, "BuildArtifact")
	assertFactType(t, bundle, "Deployment")
	if !hasPipelineStep(bundle, "gitlab-ci", "stage", "build") {
		t.Fatalf("expected gitlab stage extraction")
	}
	if !hasPipelineStep(bundle, "gitlab-ci", "run", "go build") {
		t.Fatalf("expected gitlab run command extraction")
	}
	if !hasPipelineStep(bundle, "jenkins", "stage", "Build") {
		t.Fatalf("expected jenkins stage extraction")
	}
	if !hasPipelineStep(bundle, "jenkins", "run", "docker build") {
		t.Fatalf("expected jenkins run command extraction")
	}
}

func TestM6ConfigEnvironmentAndSensitiveExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, ".env.prod", "DB_PASSWORD=super-secret\nLOG_LEVEL=info\n")
	mustWrite(t, root, "config/application-staging.yaml", "api_key: ${API_KEY}\nfeature_flag: true\n")
	mustWrite(t, root, ".github/workflows/ci.yml", "jobs:\n  test:\n    steps:\n      - run: echo hi\n      - run: echo ${{ secrets.DEPLOY_TOKEN }}\n")
	mustWrite(t, root, "app/main.go", "package main\nimport \"os\"\nfunc main(){ _ = os.Getenv(\"AUTH_TOKEN\") }\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m6"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	assertFactType(t, bundle, "ConfigKey")
	assertFactType(t, bundle, "SensitiveSurface")
	if !hasConfigKeyWithEnv(bundle, "DB_PASSWORD", "prod") {
		t.Fatalf("expected DB_PASSWORD config key with prod scope")
	}
	if !hasSensitiveKind(bundle, "config_key") {
		t.Fatalf("expected sensitive surface for secret-like config key")
	}
	if !hasSensitiveKind(bundle, "pipeline_secret") {
		t.Fatalf("expected sensitive surface for pipeline secret reference")
	}
}

func TestM6SpringProfileMergeAndCodeRefResolution(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/resources/application.yml", `
service:
  url: https://default.local
queue:
  name: ${QUEUE_NAME:orders-default}
`)
	mustWrite(t, root, "src/main/resources/application-local.yml", `
service:
  url: https://local.local
`)
	mustWrite(t, root, "src/main/resources/application-prod.yml", `
service:
  url: https://prod.local
queue:
  name: ${QUEUE_NAME}
`)
	mustWrite(t, root, "src/main/java/com/acme/ConfigUser.java", `
package com.acme;
import org.springframework.beans.factory.annotation.Value;
class ConfigUser {
  @Value("${service.url}")
  String serviceURL;
  @Value("${queue.name}")
  String queueName;
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m6-spring", "--extractors", "config"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	if !hasConfigPatternAndProfile(bundle, "service.url", "spring_profile_resolved", "local") {
		t.Fatalf("expected local profile resolved config for service.url")
	}
	if !hasConfigPatternAndProfile(bundle, "service.url", "spring_profile_resolved", "prod") {
		t.Fatalf("expected prod profile resolved config for service.url")
	}
	if !hasConfigPatternAndProfile(bundle, "queue.name", "spring_code_ref_resolved", "prod") {
		t.Fatalf("expected code-ref resolution link for queue.name in prod profile")
	}
	if !hasConfigPatternAndProfile(bundle, "queue.name", "spring_code_ref_resolved", "local") {
		t.Fatalf("expected code-ref resolution link for queue.name in local profile")
	}

	reportData, err := os.ReadFile(filepath.Join(out, "analyzers", "report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var report Report
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if strings.TrimSpace(report.ResolvedConfigProfilesPath) == "" || strings.TrimSpace(report.ResolvedConfigProfilesSHA256) == "" {
		t.Fatalf("expected resolved config profile artifact metadata in report")
	}
	if _, err := os.Stat(report.ResolvedConfigProfilesPath); err != nil {
		t.Fatalf("expected resolved config profile artifact: %v", err)
	}

	artifactData, err := os.ReadFile(report.ResolvedConfigProfilesPath)
	if err != nil {
		t.Fatalf("read resolved config profile artifact: %v", err)
	}
	var artifact resolvedConfigProfilesArtifact
	if err := json.Unmarshal(artifactData, &artifact); err != nil {
		t.Fatalf("unmarshal resolved config profile artifact: %v", err)
	}
	if _, ok := artifact.Profiles["local"]; !ok {
		t.Fatalf("expected local profile in resolved artifact")
	}
	if _, ok := artifact.Profiles["prod"]; !ok {
		t.Fatalf("expected prod profile in resolved artifact")
	}
	if _, ok := artifact.Profiles["prod"].Resolved["service.url"]; !ok {
		t.Fatalf("expected service.url resolved value in prod profile artifact")
	}
}

func TestM6SpringMultiDocumentProfileActivationAndPlaceholderMarkers(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "src/main/resources/application.yml", `
service:
  url: "https://${HOST:default.local}/api/${PATH_SEGMENT}"
---
spring:
  config:
    activate:
      on-profile: stage
queue:
  name: stage-jobs
`)
	mustWrite(t, root, "src/main/java/com/acme/ConfigUser.java", `
package com.acme;
import org.springframework.beans.factory.annotation.Value;
class ConfigUser {
  @Value("${service.url}")
  String serviceURL;
  @Value("${queue.name}")
  String queueName;
}
`)

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m6-spring-multi", "--extractors", "config"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	if !hasConfigPatternAndProfile(bundle, "queue.name", "spring_profile_resolved", "stage") {
		t.Fatalf("expected stage profile activation for queue.name")
	}
	if !hasConfigPatternStatus(bundle, "service.url", "spring_profile_resolved", "default", "partially_resolved") {
		t.Fatalf("expected partially_resolved placeholder status for service.url")
	}
	if !hasConfigPatternUnresolvedVars(bundle, "service.url", "spring_profile_resolved", "default", "PATH_SEGMENT") {
		t.Fatalf("expected unresolved placeholder marker for PATH_SEGMENT")
	}
}

func TestM7DependencyOwnershipAndRiskExtraction(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".diffmind")

	mustWrite(t, root, "go.mod", "module example.com/acme/service\n\nrequire (\n\texample.com/acme/lib v1.2.3\n\tgithub.com/gin-gonic/gin v1.10.0\n)\n")
	mustWrite(t, root, "package.json", "{\n  \"name\": \"@acme/service\",\n  \"dependencies\": {\"axios\": \"^1.7.0\", \"@acme/shared\": \"1.0.0\"},\n  \"devDependencies\": {\"typescript\": \"5.5.4\"}\n}\n")
	mustWrite(t, root, "requirements.txt", "requests>=2.0\n")
	mustWrite(t, root, "pom.xml", `
<project>
  <groupId>com.acme</groupId>
  <artifactId>svc</artifactId>
  <version>1.0.0</version>
  <dependencies>
    <dependency>
      <groupId>com.acme</groupId>
      <artifactId>internal-core</artifactId>
      <version>1.2.3</version>
    </dependency>
    <dependency>
      <groupId>org.slf4j</groupId>
      <artifactId>slf4j-api</artifactId>
      <version>2.0.13</version>
    </dependency>
  </dependencies>
</project>
`)
	mustWrite(t, root, "CODEOWNERS", "/go/ @platform-team\n/package.json @frontend-team\n")

	if err := Run(context.Background(), []string{"--source", root, "--out", out, "--snapshot-id", "snap-m7"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(out, "analyzers", "bundle.json"))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	var bundle facts.Bundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}

	assertFactType(t, bundle, "Dependency")
	assertFactType(t, bundle, "OwnershipRule")
	assertFactType(t, bundle, "DependencyRisk")
	if !hasDependency(bundle, "npm", "axios") {
		t.Fatalf("expected npm dependency axios")
	}
	if !hasDependency(bundle, "go", "github.com/gin-gonic/gin") {
		t.Fatalf("expected go dependency github.com/gin-gonic/gin")
	}
	if !hasDependencyInternal(bundle, "go", "example.com/acme/lib", true) {
		t.Fatalf("expected internal go dependency example.com/acme/lib")
	}
	if !hasDependencyInternal(bundle, "npm", "@acme/shared", true) {
		t.Fatalf("expected internal npm dependency @acme/shared")
	}
	if !hasDependencyInternal(bundle, "maven", "com.acme:internal-core", true) {
		t.Fatalf("expected internal maven dependency com.acme:internal-core")
	}
	if !hasDependencyInternal(bundle, "maven", "org.slf4j:slf4j-api", false) {
		t.Fatalf("expected external maven dependency org.slf4j:slf4j-api")
	}
	if !hasOwnership(bundle, "@frontend-team") {
		t.Fatalf("expected ownership rule for @frontend-team")
	}
	if !hasDependencyRisk(bundle, "axios") {
		t.Fatalf("expected dependency risk for floating axios version")
	}
}

func assertFactType(t *testing.T, bundle facts.Bundle, factType string) {
	t.Helper()
	for _, f := range bundle.Facts {
		if f.Type == factType {
			return
		}
	}
	t.Fatalf("missing fact type %s", factType)
}

func assertExternalCallProtocol(t *testing.T, bundle facts.Bundle, protocol string) {
	t.Helper()
	for _, f := range bundle.Facts {
		if f.Type != "ExternalCall" {
			continue
		}
		if p, ok := f.Attributes["protocol"].(string); ok && p == protocol {
			return
		}
	}
	t.Fatalf("missing external call protocol %s", protocol)
}

func assertNoFactType(t *testing.T, bundle facts.Bundle, factType string) {
	t.Helper()
	for _, f := range bundle.Facts {
		if f.Type == factType {
			t.Fatalf("unexpected fact type %s", factType)
		}
	}
}

func hasEndpoint(bundle facts.Bundle, method string, path string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "Endpoint" {
			continue
		}
		m, _ := f.Attributes["method"].(string)
		p, _ := f.Attributes["path"].(string)
		if m == method && p == path {
			return true
		}
	}
	return false
}

func hasExternalCall(bundle facts.Bundle, method string, target string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ExternalCall" {
			continue
		}
		m, _ := f.Attributes["method"].(string)
		t, _ := f.Attributes["target"].(string)
		if m == method && t == target {
			return true
		}
	}
	return false
}

func hasExternalCallWithProtocolAndMethod(bundle facts.Bundle, protocol string, method string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ExternalCall" {
			continue
		}
		p, _ := f.Attributes["protocol"].(string)
		m, _ := f.Attributes["method"].(string)
		if p == protocol && m == method {
			return true
		}
	}
	return false
}

func hasFactWithLanguage(bundle facts.Bundle, factType string, language string) bool {
	for _, f := range bundle.Facts {
		if f.Type != factType {
			continue
		}
		lang, _ := f.Attributes["language"].(string)
		if lang == language {
			return true
		}
	}
	return false
}

func hasFactTypeAndAttr(bundle facts.Bundle, factType string, key string, want string) bool {
	for _, f := range bundle.Facts {
		if f.Type != factType {
			continue
		}
		got, _ := f.Attributes[key].(string)
		if strings.TrimSpace(got) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}

func hasConfigKeyWithEnv(bundle facts.Bundle, key string, env string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ConfigKey" {
			continue
		}
		k, _ := f.Attributes["key"].(string)
		e, _ := f.Attributes["environment"].(string)
		if k == key && e == env {
			return true
		}
	}
	return false
}

func hasConfigPatternAndProfile(bundle facts.Bundle, key string, pattern string, profile string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ConfigKey" {
			continue
		}
		k, _ := f.Attributes["key"].(string)
		p, _ := f.Attributes["pattern"].(string)
		pr, _ := f.Attributes["profile"].(string)
		if k == key && p == pattern && pr == profile {
			return true
		}
	}
	return false
}

func hasConfigPatternStatus(bundle facts.Bundle, key string, pattern string, profile string, status string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ConfigKey" {
			continue
		}
		k, _ := f.Attributes["key"].(string)
		p, _ := f.Attributes["pattern"].(string)
		pr, _ := f.Attributes["profile"].(string)
		st, _ := f.Attributes["placeholder_status"].(string)
		if k == key && p == pattern && pr == profile && st == status {
			return true
		}
	}
	return false
}

func hasConfigPatternUnresolvedVars(bundle facts.Bundle, key string, pattern string, profile string, wantVar string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "ConfigKey" {
			continue
		}
		k, _ := f.Attributes["key"].(string)
		p, _ := f.Attributes["pattern"].(string)
		pr, _ := f.Attributes["profile"].(string)
		unresolved, _ := f.Attributes["placeholder_unresolved_vars"].(string)
		if k == key && p == pattern && pr == profile {
			for _, part := range strings.Split(unresolved, ",") {
				if strings.TrimSpace(part) == wantVar {
					return true
				}
			}
		}
	}
	return false
}

func hasSensitiveKind(bundle facts.Bundle, kind string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "SensitiveSurface" {
			continue
		}
		k, _ := f.Attributes["kind"].(string)
		if k == kind {
			return true
		}
	}
	return false
}

func hasDependency(bundle facts.Bundle, ecosystem string, name string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "Dependency" {
			continue
		}
		e, _ := f.Attributes["ecosystem"].(string)
		n, _ := f.Attributes["name"].(string)
		if e == ecosystem && n == name {
			return true
		}
	}
	return false
}

func hasDependencyInternal(bundle facts.Bundle, ecosystem string, name string, internal bool) bool {
	for _, f := range bundle.Facts {
		if f.Type != "Dependency" {
			continue
		}
		e, _ := f.Attributes["ecosystem"].(string)
		n, _ := f.Attributes["name"].(string)
		i, _ := f.Attributes["internal"].(bool)
		if e == ecosystem && n == name && i == internal {
			return true
		}
	}
	return false
}

func hasOwnership(bundle facts.Bundle, owner string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "OwnershipRule" {
			continue
		}
		o, _ := f.Attributes["owner"].(string)
		if o == owner {
			return true
		}
	}
	return false
}

func hasDependencyRisk(bundle facts.Bundle, name string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "DependencyRisk" {
			continue
		}
		n, _ := f.Attributes["name"].(string)
		if n == name {
			return true
		}
	}
	return false
}

func hasPipelineStep(bundle facts.Bundle, provider string, kind string, valueContains string) bool {
	for _, f := range bundle.Facts {
		if f.Type != "PipelineStep" {
			continue
		}
		p, _ := f.Attributes["provider"].(string)
		k, _ := f.Attributes["kind"].(string)
		v, _ := f.Attributes["value"].(string)
		if p == provider && k == kind && strings.Contains(v, valueContains) {
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
