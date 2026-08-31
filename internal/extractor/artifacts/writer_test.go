package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/extractor/extraction"
	"github.com/mohammad-safakhou/diffmind/internal/extractor/model"
	"github.com/mohammad-safakhou/diffmind/protocol"
)

func TestWriteDoesNotEmitLegacyArtifactDirectories(t *testing.T) {
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

	for _, legacyDir := range []string{"exposures", "dependencies", "connections", "unresolved"} {
		if _, err := os.Stat(filepath.Join(baseDir, "run1", legacyDir)); !os.IsNotExist(err) {
			t.Fatalf("legacy artifact dir %s should not be emitted; stat err=%v", legacyDir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(baseDir, "run1", ProtocolServiceJSON)); err != nil {
		t.Fatalf("canonical Protocol json missing: %v", err)
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
		"discovery":   2,
		"connections": 1,
		"validation":  1,
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

func TestWriteEmitsProtocolServiceContext(t *testing.T) {
	baseDir := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "diffmind-configuration.yaml"), []byte("service:\n  id: orders-api\n  name: orders-api\n  team: growth\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID: "legacy-exp", Type: "http_route", Name: "POST /orders", Service: repo, Platform: "http",
		Summary: "create order", Confidence: 1, Tags: []string{"deterministic"},
		Inputs:    []model.InputSpec{{Name: "orderId", Type: "body", Required: true}},
		Details:   map[string]any{"method": "POST", "path": "/orders", "body_fields": []string{"orderId"}},
		Locations: []model.Location{{File: "controller.go", StartLine: 10, EndLine: 20}},
		Evidence:  []model.Evidence{{Location: model.Location{File: "controller.go", StartLine: 10, EndLine: 20}, Source: "deterministic_ast", Snippet: "POST /orders"}},
	}}
	dep := model.Dependency{BaseEntity: model.BaseEntity{
		ID: "legacy-dep", Type: "db_operation", Name: "write orders", Service: repo, Platform: "postgres",
		Summary: "insert order", Confidence: 1, Tags: []string{"deterministic"},
		Details:   map[string]any{"table": "orders", "operation": "write", "writes": []string{"order_id"}},
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
	f, err := os.Open(filepath.Join(baseDir, "run-protocol", ProtocolServiceJSON))
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
	if len(doc.Flows[0].DataDependencies) != 1 {
		t.Fatalf("expected one data dependency, got %+v", doc.Flows[0].DataDependencies)
	}
	if got := doc.Flows[0].DataDependencies[0]; got.From.Expression != "request.body.orderId" || got.To.Expression != "orders.order_id" || got.Kind != "value_flow" {
		t.Fatalf("unexpected data dependency: %+v", got)
	}
	if len(doc.Observations) == 0 || len(doc.Evidence) == 0 {
		t.Fatalf("expected observations/evidence: obs=%+v ev=%+v", doc.Observations, doc.Evidence)
	}
	if err := protocol.ValidateCanonical(doc); err != nil {
		t.Fatalf("canonical Protocol validation failed: %v", err)
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
	for _, legacyDir := range []string{"exposures", "dependencies", "connections", "unresolved"} {
		if _, err := os.Stat(filepath.Join(baseDir, "run-protocol", legacyDir)); !os.IsNotExist(err) {
			t.Fatalf("legacy artifact dir %s should not be emitted; stat err=%v", legacyDir, err)
		}
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

func TestBuildProtocolMergesDuplicateDBResourceIDs(t *testing.T) {
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

	doc, err := buildProtocol(WriteInput{
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
		t.Fatalf("build Protocol: %v", err)
	}
	if len(doc.Objects.DBResources) != 1 {
		t.Fatalf("expected merged db resource, got %+v", doc.Objects.DBResources)
	}
	if len(doc.Objects.DBQueries) != 2 {
		t.Fatalf("expected semantic db query dedup, got %+v", doc.Objects.DBQueries)
	}
	if err := protocol.ValidateCanonical(doc); err != nil {
		t.Fatalf("canonical Protocol validation failed: %v", err)
	}
}

func TestBuildProtocolAllowsCommandExecWithoutFlow(t *testing.T) {
	repo := t.TempDir()
	dep := model.Dependency{BaseEntity: model.BaseEntity{
		ID:         "dep-script",
		Type:       "command_exec",
		Name:       "run import script",
		Platform:   "shell",
		Summary:    "executes a helper script",
		Confidence: 1,
		Tags:       []string{"deterministic"},
		Details:    map[string]any{"command": "bin/import"},
		Locations:  []model.Location{{File: "Makefile", StartLine: 12, EndLine: 12}},
		Evidence: []model.Evidence{{
			Location: model.Location{File: "Makefile", StartLine: 12, EndLine: 12},
			Snippet:  "bin/import",
			Source:   "deterministic_ast",
		}},
	}}

	doc, err := buildProtocol(WriteInput{
		RunID:        "run-command-exec",
		RepoPath:     repo,
		FinishedAt:   time.Now().UTC(),
		Dependencies: []model.Dependency{dep},
	}, model.RunManifest{Pipeline: "deterministic", SchemaVersion: protocol.SchemaServiceV1})
	if err != nil {
		t.Fatalf("build Protocol: %v", err)
	}
	if len(doc.Objects.CLICommands) != 1 {
		t.Fatalf("expected command_exec in cli_commands, got %+v", doc.Objects.CLICommands)
	}
	if doc.Objects.CLICommands[0].Kind != "command_exec" {
		t.Fatalf("kind = %q, want command_exec", doc.Objects.CLICommands[0].Kind)
	}
	if len(doc.Flows) != 0 {
		t.Fatalf("expected no flows for dependency-only document, got %+v", doc.Flows)
	}
	if err := protocol.ValidateCanonical(doc); err != nil {
		t.Fatalf("canonical Protocol validation failed: %v", err)
	}
}

func TestBuildProtocolEmitsFieldDataDependenciesForActionTypes(t *testing.T) {
	repo := t.TempDir()
	exp := model.Exposure{BaseEntity: model.BaseEntity{
		ID:         "exp-campaign",
		Type:       "http_route",
		Name:       "GET /campaigns/{campaignId}",
		Platform:   "http",
		Summary:    "get campaign",
		Confidence: 1,
		Tags:       []string{"deterministic"},
		Inputs: []model.InputSpec{
			{Name: "campaignId", Type: "path", Required: true},
			{Name: "storeCode", Type: "query", Required: false},
		},
		Details:   map[string]any{"method": "GET", "path": "/campaigns/{campaignId}", "query_params": []string{"storeCode"}},
		Locations: []model.Location{{File: "campaign.go", StartLine: 10, EndLine: 20}},
		Evidence:  []model.Evidence{{Location: model.Location{File: "campaign.go", StartLine: 10, EndLine: 20}, Source: "deterministic_ast", Snippet: "GET /campaigns/{campaignId}"}},
	}}
	deps := []model.Dependency{
		{BaseEntity: model.BaseEntity{
			ID: "http-dep", Type: "outbound_http", Name: "GET score", Platform: "http", Instance: "score-api",
			Summary: "call score", Confidence: 1, Tags: []string{"deterministic"},
			Details:   map[string]any{"method": "GET", "target_service": "score-api", "url_template": "http://score-api/scores/{storeCode}", "path_params": []string{"storeCode"}},
			Locations: []model.Location{{File: "client.go", StartLine: 30, EndLine: 35}},
			Evidence:  []model.Evidence{{Location: model.Location{File: "client.go", StartLine: 30, EndLine: 35}, Source: "deterministic_ast", Snippet: "score call"}},
		}},
		{BaseEntity: model.BaseEntity{
			ID: "queue-dep", Type: "queue_publish", Name: "campaign events", Platform: "kafka", Instance: "campaign-events",
			Summary: "publish campaign event", Confidence: 1, Tags: []string{"deterministic"},
			Details:   map[string]any{"topic": "campaign-events", "message_fields": []string{"campaignId"}},
			Locations: []model.Location{{File: "publisher.go", StartLine: 40, EndLine: 45}},
			Evidence:  []model.Evidence{{Location: model.Location{File: "publisher.go", StartLine: 40, EndLine: 45}, Source: "deterministic_ast", Snippet: "publish"}},
		}},
		{BaseEntity: model.BaseEntity{
			ID: "cache-dep", Type: "cache_operation", Name: "read campaign cache", Platform: "redis", Instance: "campaign-cache",
			Summary: "read campaign cache", Confidence: 1, Tags: []string{"deterministic"},
			Details:   map[string]any{"cache": "campaign-cache", "operation": "read", "key_pattern": "campaign:{campaignId}"},
			Locations: []model.Location{{File: "cache.go", StartLine: 50, EndLine: 55}},
			Evidence:  []model.Evidence{{Location: model.Location{File: "cache.go", StartLine: 50, EndLine: 55}, Source: "deterministic_ast", Snippet: "cache read"}},
		}},
		{BaseEntity: model.BaseEntity{
			ID: "workflow-dep", Type: "workflow_orchestration", Name: "campaign workflow", Platform: "camunda", Instance: "camunda",
			Summary: "send campaign variable", Confidence: 1, Tags: []string{"deterministic"},
			Details:   map[string]any{"orchestrator": "camunda", "topic": "campaign", "variables": []string{"campaignId"}},
			Locations: []model.Location{{File: "workflow.go", StartLine: 60, EndLine: 65}},
			Evidence:  []model.Evidence{{Location: model.Location{File: "workflow.go", StartLine: 60, EndLine: 65}, Source: "deterministic_ast", Snippet: "workflow"}},
		}},
	}
	var conns []model.Connection
	for _, dep := range deps {
		conns = append(conns, model.Connection{
			ID: dep.ID + "-conn", FromExposureID: exp.ID, ToDependencyID: dep.ID,
			FromType: exp.Type, ToType: dep.Type, Confidence: 1,
			Condition: model.Condition{Kind: "unconditional", Expression: "true"},
		})
	}
	doc, err := buildProtocol(WriteInput{
		RunID:        "run-data-deps",
		RepoPath:     repo,
		FinishedAt:   time.Now().UTC(),
		Exposures:    []model.Exposure{exp},
		Dependencies: deps,
		Connections:  conns,
	}, model.RunManifest{Pipeline: "deterministic", SchemaVersion: protocol.SchemaServiceV1})
	if err != nil {
		t.Fatalf("build Protocol: %v", err)
	}
	byTarget := map[string]protocol.DataDependency{}
	for _, flow := range doc.Flows {
		if len(flow.DataDependencies) != 1 {
			t.Fatalf("expected one data dependency per flow, got flow=%s deps=%+v", flow.ID, flow.DataDependencies)
		}
		byTarget[flow.DataDependencies[0].To.ObjectRef] = flow.DataDependencies[0]
	}
	assertDep := func(target, fromExpr, toExpr, kind string) {
		t.Helper()
		dep, ok := byTarget[target]
		if !ok {
			t.Fatalf("missing data dependency for %s; all=%+v", target, byTarget)
		}
		if dep.From.Expression != fromExpr || dep.To.Expression != toExpr || dep.Kind != kind {
			t.Fatalf("unexpected data dependency for %s: %+v", target, dep)
		}
	}
	assertDep("httpcall.get_score_api", "request.query.storeCode", "path.storeCode", "path_flow")
	assertDep("queue.publish_campaign_events", "request.path.campaignId", "message.campaignId", "payload_flow")
	assertDep("cache.read_campaign_cache", "request.path.campaignId", "cache.key.campaignId", "key_flow")
	assertDep("workflow.camunda_campaign", "request.path.campaignId", "workflow.variables.campaignId", "value_flow")
	if err := protocol.ValidateCanonical(doc); err != nil {
		t.Fatalf("canonical Protocol validation failed: %v", err)
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
