package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/protocol"
)

func TestWriteSanitizesUnresolvedFileNames(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run1",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
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

// stageFailures groups unresolved items by the pipeline stage that
// produced them. The dashboard reads this off the manifest to render a
// per-stage health badge, so it must be present and accurate even when
// no stage hard-failed.
func TestWriteManifestStageFailureSummary(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run42",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Unresolved: []model.UnresolvedItem{
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-a", ReasonCode: "discovery_failure", Reason: "boom"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-b", ReasonCode: "discovery_failure", Reason: "boom"},
			{Kind: model.KindDependency, Type: "connection", Name: "exp-1", ReasonCode: "connections_failure", Reason: "boom"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-d", ReasonCode: "reexamine_failure", Reason: "kept original seed"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-e", ReasonCode: "rejected_on_reexamination", Reason: "not real"},
			{Kind: model.KindExposure, Type: "http_route", Name: "obj-f", ReasonCode: "missing_required_details", Reason: "no path"},
		},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(baseDir, "run42", "run_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got model.RunManifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]int{
		"discovery":     2,
		"connections":   1,
		"reexamination": 2,
		"validation":    1,
	}
	if len(got.StageFailures) != len(want) {
		t.Fatalf("stage_failures keys mismatch: got %v want %v", got.StageFailures, want)
	}
	for k, v := range want {
		if got.StageFailures[k] != v {
			t.Fatalf("stage_failures[%q] = %d, want %d (full: %v)", k, got.StageFailures[k], v, got.StageFailures)
		}
	}
}

// When there is no unresolved diagnostic the StageFailures field must
// be omitted (nil), so a successful run's manifest stays clean.
func TestWriteManifestNoStageFailuresWhenAllGreen(t *testing.T) {
	baseDir := t.TempDir()
	_, err := Write(WriteInput{
		RunID:         "run-green",
		BaseDir:       baseDir,
		RepoPath:      "/repo",
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(baseDir, "run-green", "run_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got model.RunManifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.StageFailures != nil {
		t.Fatalf("expected stage_failures omitted for clean run; got %v", got.StageFailures)
	}
}

func TestWriteManifestIncludesTeamAndRepoMetrics(t *testing.T) {
	baseDir := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "diffmind-configuration.yaml"), []byte("service:\n  team: growth\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.Mkdir(filepath.Join(repo, "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir skipped dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "node_modules", "ignored.js"), []byte("const noisy = true\n"), 0o644); err != nil {
		t.Fatalf("write ignored source: %v", err)
	}

	_, err := Write(WriteInput{
		RunID:         "run-metrics",
		BaseDir:       baseDir,
		RepoPath:      repo,
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		RepoFacts: &extraction.RepoFacts{
			ServiceName:   "orders",
			Frameworks:    []string{"net/http"},
			LanguageFacts: []extraction.LanguageFact{{Language: "go", BuildTool: "go"}},
		},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(baseDir, "run-metrics", "run_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got model.RunManifest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Team != "growth" {
		t.Fatalf("team = %q, want growth", got.Team)
	}
	if got.RepoMetrics == nil {
		t.Fatal("repo_metrics missing")
	}
	if got.RepoMetrics.DetectedServiceName != "orders" {
		t.Fatalf("detected service = %q", got.RepoMetrics.DetectedServiceName)
	}
	if got.RepoMetrics.TotalLOC != 4 || got.RepoMetrics.FileCount != 2 {
		t.Fatalf("metrics = %+v, want 4 loc across main.go and diffmind-configuration.yaml with node_modules skipped", got.RepoMetrics)
	}
	hasGo := false
	for _, lm := range got.RepoMetrics.Languages {
		if lm.Language == "go" && lm.Files == 1 && lm.LOC == 2 {
			hasGo = true
		}
	}
	if !hasGo {
		t.Fatalf("language metrics = %+v", got.RepoMetrics.Languages)
	}
}

func TestWriteEmitsDiffMind protocolServiceContext(t *testing.T) {
	baseDir := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "diffmind-configuration.yaml"), []byte("service:\n  id: orders-api\n  name: orders-api\n  team: growth\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID: "legacy-exp", Type: "http_route", Name: "POST /orders", Service: repo, Platform: "http",
		Summary: "create order", Confidence: 1, Tags: []string{"deterministic"},
		Details:   map[string]any{"method": "POST", "path": "/orders"},
		Locations: []model.Location{{File: "controller.go", StartLine: 10, EndLine: 20}},
		Evidence:  []model.Evidence{{Location: model.Location{File: "controller.go", StartLine: 10, EndLine: 20}, Source: "deterministic_ast", Snippet: "POST /orders"}},
	}}
	dep := model.Dependency{BaseEntity: model.BaseEntity{
		ID: "legacy-dep", Type: "db_operation", Name: "write orders", Service: repo, Platform: "postgres",
		Summary: "insert order", Confidence: 1, Tags: []string{"deterministic"},
		Details:   map[string]any{"table": "orders", "operation": "write"},
		Locations: []model.Location{{File: "repo.go", StartLine: 30, EndLine: 40}},
		Evidence:  []model.Evidence{{Location: model.Location{File: "repo.go", StartLine: 30, EndLine: 40}, Source: "deterministic_ast", Snippet: "insert orders"}},
	}}
	httpDep := model.Dependency{BaseEntity: model.BaseEntity{
		ID: "legacy-http-dep", Type: "outbound_http", Name: "GET /charge", Service: repo, Platform: "http", Instance: "billing-service",
		Summary: "call billing", Confidence: 1, Tags: []string{"deterministic"},
		InstanceRef: &model.InstanceRef{Kind: "http", LogicalName: "billing-service", URLTemplate: "${BILLING_URL:https://billing-service.internal}", Host: "billing-service.internal", ConfigSource: "services.billing-service.url"},
		Details:     map[string]any{"method": "GET", "path": "/charge", "target_service": "billing-service", "url_template": "${BILLING_URL:https://billing-service.internal}"},
		Locations:   []model.Location{{File: "client.go", StartLine: 50, EndLine: 55}},
		Evidence:    []model.Evidence{{Location: model.Location{File: "client.go", StartLine: 50, EndLine: 55}, Source: "deterministic_ast", Snippet: "GET /charge"}},
	}}
	_, err := Write(WriteInput{
		RunID:         "run-protocol",
		BaseDir:       baseDir,
		RepoPath:      repo,
		MinConfidence: 0.7,
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Exposures:     []model.Exposure{exp},
		Dependencies:  []model.Dependency{dep, httpDep},
		Connections: []model.Connection{{
			ID: "legacy-conn", FromExposureID: "legacy-exp", ToDependencyID: "legacy-dep",
			FromType: "http_route", ToType: "db_operation", Confidence: 1,
			Condition: model.Condition{Kind: "unconditional", Expression: "true"},
		}},
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(filepath.Join(baseDir, "run-protocol", DiffMind protocolServiceJSON))
	if err != nil {
		t.Fatalf("open protocol json: %v", err)
	}
	defer f.Close()
	doc, err := protocol.DecodeJSON(f)
	if err != nil {
		t.Fatalf("decode protocol: %v", err)
	}
	if doc.Service.ID != "orders_api" || doc.Service.Team != "growth" {
		t.Fatalf("unexpected service: %+v", doc.Service)
	}
	if len(doc.Objects.HTTPEndpoints) != 1 || doc.Objects.HTTPEndpoints[0].ID != "http.post_orders" {
		t.Fatalf("unexpected http endpoints: %+v", doc.Objects.HTTPEndpoints)
	}
	if len(doc.Objects.DBQueries) != 1 || len(doc.Objects.DBResources) != 1 {
		t.Fatalf("expected db query/resource, got queries=%+v resources=%+v", doc.Objects.DBQueries, doc.Objects.DBResources)
	}
	if len(doc.Objects.HTTPCalls) != 1 {
		t.Fatalf("expected one http call, got %+v", doc.Objects.HTTPCalls)
	}
	if target := doc.Objects.HTTPCalls[0].Target; target == nil || target.Type != "service" || target.Ref != "service.billing-service" || target.Unresolved {
		t.Fatalf("unexpected http target: %+v", target)
	}
	if doc.Objects.HTTPCalls[0].URLTemplate != "${BILLING_URL:https://billing-service.internal}" {
		t.Fatalf("unexpected http url_template: %q", doc.Objects.HTTPCalls[0].URLTemplate)
	}
	if len(doc.Flows) != 1 || doc.Flows[0].From != "http.post_orders" || doc.Flows[0].To != "dbq.create_orders" {
		t.Fatalf("unexpected flows: %+v", doc.Flows)
	}
	if len(doc.Observations) == 0 || len(doc.Evidence) == 0 {
		t.Fatalf("expected observations/evidence: obs=%+v ev=%+v", doc.Observations, doc.Evidence)
	}
	if err := protocol.ValidateCanonical(doc); err != nil {
		t.Fatalf("canonical DiffMind protocol validation failed: %v", err)
	}
	if doc.Objects.HTTPCalls == nil || doc.Objects.QueueConsumers == nil || doc.Objects.CacheOperations == nil {
		t.Fatalf("expected canonical empty object arrays, got %+v", doc.Objects)
	}
	if len(doc.Objects.DBResources[0].Observations) == 0 || len(doc.Objects.DBResources[0].EvidenceRefs) == 0 {
		t.Fatalf("db resource common refs must inherit evidence: %+v", doc.Objects.DBResources[0].ObjectiveBase)
	}
	if got := doc.Objects.DBQueries[0].Metadata["plugin_source"]; got == "" {
		t.Fatalf("deterministic plugin_source must be detector-specific")
	}
	depBytes, err := os.ReadFile(filepath.Join(baseDir, "run-protocol", "dependencies", "db_operation.json"))
	if err != nil {
		t.Fatalf("read legacy dependency file: %v", err)
	}
	var legacyDeps []model.Dependency
	if err := json.Unmarshal(depBytes, &legacyDeps); err != nil {
		t.Fatalf("decode legacy dependency file: %v", err)
	}
	if len(legacyDeps) != 1 {
		t.Fatalf("expected one legacy db dependency, got %+v", legacyDeps)
	}
	if legacyDeps[0].PluginSource == "" {
		t.Fatalf("legacy deterministic plugin_source must be detector-specific: %+v", legacyDeps[0])
	}
	b, err := os.ReadFile(filepath.Join(baseDir, "run-protocol", "run_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest model.RunManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.SchemaVersion != protocol.SchemaServiceV1 || manifest.Pipeline != "deterministic" {
		t.Fatalf("manifest schema/pipeline = %q/%q, want %q/deterministic", manifest.SchemaVersion, manifest.Pipeline, protocol.SchemaServiceV1)
	}
}

func TestBuildDiffMind protocolMergesDuplicateDBResourceIDs(t *testing.T) {
	repo := t.TempDir()
	mkDep := func(id, platform, operation string) model.Dependency {
		return model.Dependency{BaseEntity: model.BaseEntity{
			ID:         id,
			Type:       "db_operation",
			Name:       operation + " orders",
			Platform:   platform,
			Operation:  operation,
			Confidence: 1,
			Tags:       []string{"deterministic"},
			Details: map[string]any{
				"table":     "orders",
				"operation": operation,
			},
			Locations: []model.Location{{File: "repo.go", StartLine: 10, EndLine: 11}},
			Evidence: []model.Evidence{{
				Location: model.Location{File: "repo.go", StartLine: 10, EndLine: 11},
				Snippet:  "repo read",
				Source:   "deterministic_ast",
			}},
		}}
	}

	doc, err := buildDiffMind protocol(WriteInput{
		RunID:      "run-merge-db",
		RepoPath:   repo,
		FinishedAt: time.Now().UTC(),
		Dependencies: []model.Dependency{
			mkDep("dep-generic", "database", "read"),
			mkDep("dep-postgres", "postgres", "write"),
			mkDep("dep-postgres-duplicate", "postgres", "write"),
		},
	}, model.RunManifest{Pipeline: "deterministic", SchemaVersion: protocol.SchemaServiceV1})
	if err != nil {
		t.Fatalf("build DiffMind protocol: %v", err)
	}
	if len(doc.Objects.DBResources) != 1 {
		t.Fatalf("expected merged db resource, got %+v", doc.Objects.DBResources)
	}
	if len(doc.Objects.DBQueries) != 2 {
		t.Fatalf("expected semantic db query dedup, got %+v", doc.Objects.DBQueries)
	}
	if err := protocol.ValidateCanonical(doc); err != nil {
		t.Fatalf("canonical DiffMind protocol validation failed: %v", err)
	}
}

func TestIgnoredDeterministicDirtyPath(t *testing.T) {
	ignored := []string{
		"?? .diffmind/",
		"?? .diffmind/context/service.json",
		"?? .project",
		"?? module/.classpath",
		"?? module/.factorypath",
		"?? module/.settings/org.eclipse.jdt.core.prefs",
		"?? diffmind.yaml",
		"?? diffmind.curated.yaml",
	}
	for _, line := range ignored {
		if !ignoredDeterministicDirtyPath(line) {
			t.Fatalf("expected %q to be ignored", line)
		}
	}
	if ignoredDeterministicDirtyPath("?? src/main/App.java") {
		t.Fatal("source file dirt must not be ignored")
	}
}
