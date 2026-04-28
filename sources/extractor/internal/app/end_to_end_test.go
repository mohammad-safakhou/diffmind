package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/model"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// TestEndToEndPipelineAgainstSampleRepo drives the whole pipeline against an
// in-process OpenCode stub that returns realistic responses for the fixtures
// in testdata/sample_repo. It verifies:
//   - artifacts are written to disk in the expected layout
//   - exposures, dependencies and connections all materialize
//   - the whole run completes well under the 5 minute budget
func TestEndToEndPipelineAgainstSampleRepo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(stubOpencodeHandler))
	defer srv.Close()

	// Copy the sample repo into a temp dir so we can safely assert the
	// orchestrator did not mutate it. We never run extraction directly
	// against the checked-in fixture.
	srcRepo, err := filepath.Abs(filepath.Join("..", "..", "testdata", "sample_repo"))
	if err != nil {
		t.Fatalf("resolve repo path: %v", err)
	}
	if _, err := os.Stat(srcRepo); err != nil {
		t.Fatalf("missing sample repo %s: %v", srcRepo, err)
	}
	repoPath := copyTreeForTest(t, srcRepo)
	beforeHashes := hashTreeApp(t, repoPath)

	out := t.TempDir()
	cfg := config.Default()
	cfg.OpenCode.BaseURL = srv.URL
	cfg.OpenCode.ProviderID = "test"
	cfg.OpenCode.ModelID = "test"
	cfg.OpenCode.TimeoutSec = 30
	cfg.Runtime.Workers = 8
	cfg.Runtime.MaxCatalogItems = 10
	cfg.Quality.MinConfidence = 0.7
	cfg.Artifacts.BaseDir = out

	start := time.Now()
	res, err := Run(context.Background(), RunInput{RepoPath: repoPath, Config: cfg})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("pipeline run failed: %v", err)
	}
	if elapsed > 5*time.Minute {
		t.Fatalf("pipeline exceeded 5m budget: %s", elapsed)
	}
	t.Logf("pipeline completed in %s", elapsed)
	if res.RunID == "" || res.RunDir == "" {
		t.Fatalf("expected run id and dir, got %+v", res)
	}

	// Run manifest must exist.
	manifestPath := filepath.Join(res.RunDir, "run_manifest.json")
	var manifest model.RunManifest
	readJSON(t, manifestPath, &manifest)
	if manifest.RunID != res.RunID {
		t.Fatalf("manifest run id mismatch: %s vs %s", manifest.RunID, res.RunID)
	}
	if manifest.Counts["exposures"] < 1 {
		t.Fatalf("expected at least one exposure in manifest, got counts=%+v", manifest.Counts)
	}
	if manifest.Counts["dependencies"] < 1 {
		t.Fatalf("expected at least one dependency in manifest, got counts=%+v", manifest.Counts)
	}
	if manifest.Counts["connections"] < 1 {
		t.Fatalf("expected at least one connection in manifest, got counts=%+v", manifest.Counts)
	}

	// Artifact directories must be populated.
	for _, dir := range []string{"exposures", "dependencies", "connections"} {
		entries, err := os.ReadDir(filepath.Join(res.RunDir, dir))
		if err != nil {
			t.Fatalf("reading %s dir: %v", dir, err)
		}
		if len(entries) == 0 {
			t.Fatalf("expected files in %s/, got empty", dir)
		}
	}

	// Spot-check that the http_route exposure has method/path details.
	httpRouteFile := findFile(t, filepath.Join(res.RunDir, "exposures"), "http_route")
	var httpExposures []model.Exposure
	readJSON(t, httpRouteFile, &httpExposures)
	if len(httpExposures) == 0 {
		t.Fatalf("expected at least one http_route exposure in %s", httpRouteFile)
	}
	foundPost := false
	for _, e := range httpExposures {
		if e.Details["method"] == "POST" && e.Details["path"] == "/orders" {
			foundPost = true
		}
	}
	if !foundPost {
		t.Fatalf("expected POST /orders exposure with details, got %+v", httpExposures)
	}

	// Spot-check the outbound_http dependency details propagated through Stage 3.
	outboundFile := findFile(t, filepath.Join(res.RunDir, "dependencies"), "outbound_http")
	var outboundDeps []model.Dependency
	readJSON(t, outboundFile, &outboundDeps)
	foundBilling := false
	for _, d := range outboundDeps {
		if url, ok := d.Details["target_url"].(string); ok && strings.Contains(url, "billing.internal") {
			foundBilling = true
		}
	}
	if !foundBilling {
		t.Fatalf("expected billing.internal target_url in outbound_http deps, got %+v", outboundDeps)
	}

	// Connections must be present: 2 from the http_route exposure + 1 from the scheduled job.
	connFiles, err := os.ReadDir(filepath.Join(res.RunDir, "connections"))
	if err != nil {
		t.Fatalf("reading connections dir: %v", err)
	}
	totalConns := 0
	for _, cf := range connFiles {
		var conns []model.Connection
		readJSON(t, filepath.Join(res.RunDir, "connections", cf.Name()), &conns)
		totalConns += len(conns)
	}
	if totalConns != manifest.Counts["connections"] {
		t.Fatalf("manifest says %d connections but disk has %d", manifest.Counts["connections"], totalConns)
	}

	// Critical safety invariant: the user's repo must be byte-for-byte
	// identical after the run. The orchestrator must have routed every
	// OpenCode session through a snapshot.
	afterHashes := hashTreeApp(t, repoPath)
	if len(beforeHashes) != len(afterHashes) {
		t.Fatalf("file count changed: before=%d after=%d", len(beforeHashes), len(afterHashes))
	}
	for name, h := range beforeHashes {
		if afterHashes[name] != h {
			t.Fatalf("file %s was mutated during the run", name)
		}
	}
}

// hashTreeApp is the local copy of the snapshot-isolation test helper.
// Returns a stable map of relative path -> sha256 hex.
func hashTreeApp(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// copyTreeForTest mirrors a fixture into a t.TempDir() so the test can
// assert that Run() did not modify it. It is a small, dependency-free copy
// helper used only by tests.
func copyTreeForTest(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// readJSON reads a JSON file and fatals the test on any I/O or decode error.
func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
}

func findFile(t *testing.T, dir, prefix string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no file with prefix %q in %s", prefix, dir)
	return ""
}

// ----------------------------------------------------------------------------
// Stub OpenCode server mirroring the endpoints the real client uses.
// ----------------------------------------------------------------------------

var orderExposureID = stableEntityID("exposure", "http_route", "POST /orders", "cmd/api.go", "12:18")
var dbDepID = stableEntityID("dependency", "db_operation", "orders_db_open", "cmd/api.go", "17:17")
var httpDepID = stableEntityID("dependency", "outbound_http", "POST billing/charge", "cmd/api.go", "18:18")
var cmdExecDepID = stableEntityID("dependency", "command_exec", "shell_exec_echo_run", "internal/worker.go", "7:7")
var scheduledExpID = stableEntityID("exposure", "scheduled_job", "StartCronJob", "internal/worker.go", "5:8")

// stableEntityID mirrors agents.toBase's ID derivation so the stub can refer
// to entities by the same IDs the production code will assign after
// conversion. The stub uses these IDs when returning connection catalog
// dependencies and when referencing EXPOSURE_ID in connection prompts.
func stableEntityID(kind, typ, name, file, lineRange string) string {
	return util.StableID(kind, typ, name, file, lineRange)
}

func stubOpencodeHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/global/health":
		w.WriteHeader(http.StatusOK)
		return
	case r.URL.Path == "/session" && r.Method == http.MethodPost:
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "session-1"})
		return
	case strings.HasPrefix(r.URL.Path, "/session/") && r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	case strings.HasPrefix(r.URL.Path, "/session/") && strings.HasSuffix(r.URL.Path, "/message"):
		handleStubMessage(w, r)
		return
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func handleStubMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	prompt := ""
	for _, p := range body.Parts {
		if p.Type == "text" {
			prompt += p.Text
		}
	}
	payload := stubResponseFor(prompt)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"info": map[string]any{"structured": payload},
	})
}

func stubResponseFor(prompt string) map[string]any {
	switch {
	case strings.Contains(prompt, "AGENT ROLE: repo-facts"):
		return map[string]any{
			"service_name": "sample-repo",
			"languages":    []string{"go"},
			"frameworks":   []string{"net/http"},
			"build_files":  []string{},
			"config_files": []string{},
		}

	// ---- Discovery ----
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "POST /orders", "summary": "order handler", "confidence": 0.95,
			"details":          map[string]any{"method": "POST", "path": "/orders"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 12, "end_line": 18}},
			"evidence": []any{map[string]any{
				"file": "cmd/api.go", "start_line": 12, "end_line": 12,
				"snippet": "func orderHandler(w http.ResponseWriter, r *http.Request)",
				"source":  "opencode",
			}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.scheduled_job"):
		return map[string]any{"items": []any{map[string]any{
			"type": "scheduled_job", "name": "StartCronJob", "summary": "shell-run cron", "confidence": 0.9,
			"details":          map[string]any{"schedule": "unknown", "handler": "StartCronJob"},
			"source_locations": []any{map[string]any{"file": "internal/worker.go", "start_line": 5, "end_line": 8}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "orders_db_open", "summary": "Opens postgres connection", "confidence": 0.9,
			"details":          map[string]any{"operation": "connect", "database_type": "postgres"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 17, "end_line": 17}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.outbound_http"):
		return map[string]any{"items": []any{map[string]any{
			"type": "outbound_http", "name": "POST billing/charge", "summary": "charges billing", "confidence": 0.92,
			"details":          map[string]any{"method": "POST", "path": "/charge", "target_url": "https://billing.internal/charge"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 18, "end_line": 18}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.command_exec"):
		return map[string]any{"items": []any{map[string]any{
			"type": "command_exec", "name": "shell_exec_echo_run", "summary": "runs shell echo", "confidence": 0.85,
			"details":          map[string]any{"command": "sh -c echo run"},
			"source_locations": []any{map[string]any{"file": "internal/worker.go", "start_line": 7, "end_line": 7}},
		}}}
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor"):
		return map[string]any{"items": []any{}}

	// ---- Detail ----
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "POST /orders", "summary": "Handles new order POST requests", "confidence": 0.97,
			"key_actions":      []any{"check method", "open postgres", "POST billing/charge"},
			"details":          map[string]any{"method": "POST", "path": "/orders", "handler": "orderHandler"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 12, "end_line": 18}},
			"evidence": []any{map[string]any{
				"file": "cmd/api.go", "start_line": 13, "end_line": 13,
				"snippet": "if r.Method != http.MethodPost", "source": "opencode",
			}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: exposure.scheduled_job"):
		return map[string]any{"item": map[string]any{
			"type": "scheduled_job", "name": "StartCronJob", "summary": "kicks off a shell command", "confidence": 0.9,
			"details":          map[string]any{"schedule": "manual", "handler": "StartCronJob"},
			"source_locations": []any{map[string]any{"file": "internal/worker.go", "start_line": 5, "end_line": 8}},
			"evidence": []any{map[string]any{
				"file": "internal/worker.go", "start_line": 7, "end_line": 7,
				"snippet": "exec.Command(\"sh\", \"-c\", \"echo run\")", "source": "opencode",
			}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "orders_db_open", "summary": "Opens postgres connection (no query)", "confidence": 0.92,
			"details":          map[string]any{"operation": "connect", "database_type": "postgres", "connection_string": "postgres://localhost/db"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 17, "end_line": 17}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.outbound_http"):
		return map[string]any{"item": map[string]any{
			"type": "outbound_http", "name": "POST billing/charge", "summary": "Calls billing.internal", "confidence": 0.95,
			"details":          map[string]any{"method": "POST", "path": "/charge", "target_url": "https://billing.internal/charge", "target_service": "billing"},
			"source_locations": []any{map[string]any{"file": "cmd/api.go", "start_line": 18, "end_line": 18}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor") && strings.Contains(prompt, "OBJECTIVE_ID: dependency.command_exec"):
		return map[string]any{"item": map[string]any{
			"type": "command_exec", "name": "shell_exec_echo_run", "summary": "sh -c echo run", "confidence": 0.88,
			"details":          map[string]any{"command": "sh -c echo run"},
			"source_locations": []any{map[string]any{"file": "internal/worker.go", "start_line": 7, "end_line": 7}},
		}}
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor"):
		return map[string]any{"item": nil}

	// ---- Connections ----
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor") && strings.Contains(prompt, "EXPOSURE_ID: "+orderExposureID):
		items := []any{}
		if strings.Contains(prompt, dbDepID) {
			items = append(items, map[string]any{
				"from_exposure_id": orderExposureID,
				"to_dependency_id": dbDepID,
				"summary":          "handler opens postgres",
				"confidence":       0.9,
				"condition":        map[string]any{"kind": "predicate", "expression": "r.Method == POST", "explanation": "only on POST"},
				"path_signature":   "orders->db",
			})
		}
		if strings.Contains(prompt, httpDepID) {
			items = append(items, map[string]any{
				"from_exposure_id": orderExposureID,
				"to_dependency_id": httpDepID,
				"summary":          "handler posts to billing",
				"confidence":       0.9,
				"condition":        map[string]any{"kind": "predicate", "expression": "r.Method == POST", "explanation": "only on POST"},
				"path_signature":   "orders->billing",
			})
		}
		return map[string]any{"items": items}
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor") && strings.Contains(prompt, "EXPOSURE_ID: "+scheduledExpID):
		if strings.Contains(prompt, cmdExecDepID) {
			return map[string]any{"items": []any{map[string]any{
				"from_exposure_id": scheduledExpID,
				"to_dependency_id": cmdExecDepID,
				"summary":          "cron job runs shell command",
				"confidence":       0.88,
				"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
				"path_signature":   "cron->shell",
			}}}
		}
		return map[string]any{"items": []any{}}
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor"):
		return map[string]any{"items": []any{}}

	// ---- Re-examination (only hit if discovery/detail seeds are flagged) ----
	case strings.Contains(prompt, "AGENT ROLE: reexaminer"):
		// Shouldn't trigger in the happy path because all discovery items
		// include required detail fields. Return empty to be safe.
		return map[string]any{"items": []any{}}
	}
	return map[string]any{"items": []any{}}
}
