package agents

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// ----------------------------------------------------------------------------
// Test fakes
// ----------------------------------------------------------------------------

// promptRecorder counts sessions and captures prompts per role for assertions.
type promptRecorder struct {
	mu          sync.Mutex
	createCalls int
	roles       map[string]int
	directories []string
}

func newRecorder() *promptRecorder {
	return &promptRecorder{roles: map[string]int{}}
}

func (r *promptRecorder) observeSession(directory string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCalls++
	if directory != "" {
		r.directories = append(r.directories, directory)
	}
}

func (r *promptRecorder) observeRole(role string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roles[role]++
}

func (r *promptRecorder) seenDirectories() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.directories))
	copy(out, r.directories)
	return out
}

// discoverRole infers a coarse role from the agent prompt so that tests can
// assert per-stage behavior without re-parsing the whole prompt.
func discoverRole(prompt string) string {
	switch {
	case strings.Contains(prompt, "AGENT ROLE: repo-facts"):
		return "repo_facts"
	case strings.Contains(prompt, "AGENT ROLE: objective-extractor"):
		return "discovery"
	case strings.Contains(prompt, "AGENT ROLE: reexaminer"):
		return "reexamination"
	case strings.Contains(prompt, "AGENT ROLE: detail-extractor"):
		return "detail"
	case strings.Contains(prompt, "AGENT ROLE: connection-extractor"):
		return "connection"
	default:
		return "unknown"
	}
}

// fakeOpenCode is a fully-scripted OpenCode server used by the primary happy
// path test. It returns a populated http_route exposure and a db_operation
// dependency with all required detail fields so the re-exam stage is a no-op.
type fakeOpenCode struct {
	rec *promptRecorder
}

func newFakeOpenCode() *fakeOpenCode { return &fakeOpenCode{rec: newRecorder()} }

func (f *fakeOpenCode) Enabled() bool { return true }
func (f *fakeOpenCode) CreateSession(ctx context.Context, directory string) (string, error) {
	f.rec.observeSession(directory)
	return "s", nil
}
func (f *fakeOpenCode) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeOpenCode) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	payload, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(payload)
	return string(b), nil
}

func (f *fakeOpenCode) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	dependencyID := util.StableID("dependency", "db_operation", "users_table_select", "repo.go", "40:60")
	role := discoverRole(prompt)
	f.rec.observeRole(role)

	switch {
	case role == "repo_facts":
		return map[string]any{
			"service_name": "users-api",
			"languages":    []any{"go"},
			"frameworks":   []any{},
			"build_files":  []any{"go.mod"},
			"config_files": []any{},
		}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "HTTP endpoint", "confidence": 0.95,
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true}},
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
			"evidence": []any{map[string]any{
				"file": "api.go", "start_line": 12, "end_line": 12,
				"snippet": "router.GET(\"/users/{id}\", getUser)", "source": "opencode",
			}},
		}}}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "Reads users table", "confidence": 0.94,
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true}},
			"details":          map[string]any{"table": "users", "operation": "read"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
			"evidence": []any{map[string]any{
				"file": "repo.go", "start_line": 45, "end_line": 45,
				"snippet": "SELECT * FROM users WHERE id = ?", "source": "opencode",
			}},
		}}}, nil

	case role == "discovery":
		return map[string]any{"items": []any{}}, nil

	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "Validates id and returns user", "confidence": 0.96,
			"key_actions":      []any{"validate path param", "query user repository", "return JSON"},
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true, "description": "user identifier"}},
			"details":          map[string]any{"method": "GET", "path": "/users/{id}", "auth": "required"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
			"evidence": []any{map[string]any{
				"file": "api.go", "start_line": 20, "end_line": 20,
				"snippet": "userRepo.GetByID(ctx, id)", "source": "opencode",
			}},
		}}, nil

	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "Select by id on users table", "confidence": 0.95,
			"key_actions":      []any{"prepare query", "execute read"},
			"inputs":           []any{map[string]any{"name": "id", "type": "string", "required": true}},
			"details":          map[string]any{"table": "users", "operation": "read", "transaction": "none"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
			"evidence": []any{map[string]any{
				"file": "repo.go", "start_line": 45, "end_line": 45,
				"snippet": "SELECT * FROM users WHERE id = ?", "source": "opencode",
			}},
		}}, nil

	case role == "detail":
		return map[string]any{"item": nil}, nil

	case role == "connection" && strings.Contains(prompt, "EXPOSURE_ID: "+exposureID):
		return map[string]any{"items": []any{map[string]any{
			"from_exposure_id": exposureID,
			"to_dependency_id": dependencyID,
			"summary":          "Reads users table for endpoint",
			"confidence":       0.93,
			"path_signature":   "main",
			"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
			"paths": []any{map[string]any{
				"id":        "p1",
				"summary":   "happy path",
				"condition": map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
				"steps": []any{map[string]any{
					"order": 1, "action": "query users", "operation": "read", "from": "http_route", "to": "db_operation",
					"condition": map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
					"location":  map[string]any{"file": "api.go", "start_line": 20, "end_line": 20},
					"evidence": []any{map[string]any{
						"file": "api.go", "start_line": 20, "end_line": 20,
						"snippet": "userRepo.GetByID(ctx, id)", "source": "opencode",
					}},
				}},
			}},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 20, "end_line": 20}},
			"evidence": []any{map[string]any{
				"file": "api.go", "start_line": 20, "end_line": 20,
				"snippet": "userRepo.GetByID(ctx, id)", "source": "opencode",
			}},
		}}}, nil

	case role == "connection":
		return map[string]any{"items": []any{}}, nil

	default:
		return map[string]any{"items": []any{}}, nil
	}
}

// ----------------------------------------------------------------------------
// Happy-path pipeline
// ----------------------------------------------------------------------------

func TestRunBuildsExposuresDependenciesAndConnections(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	fake := newFakeOpenCode()

	result, err := Run(context.Background(), cfg, t.TempDir(), fake)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := len(result.Exposures); got != 1 {
		t.Fatalf("expected 1 exposure, got %d", got)
	}
	if got := len(result.Dependencies); got != 1 {
		t.Fatalf("expected 1 dependency, got %d", got)
	}
	if got := len(result.Connections); got != 1 {
		t.Fatalf("expected 1 connection, got %d", got)
	}
	if result.Exposures[0].Type != "http_route" {
		t.Fatalf("unexpected exposure type: %s", result.Exposures[0].Type)
	}
	if result.Dependencies[0].Type != "db_operation" {
		t.Fatalf("unexpected dependency type: %s", result.Dependencies[0].Type)
	}
	if result.Dependencies[0].Details["table"] != "users" {
		t.Fatalf("expected dependency table details, got %v", result.Dependencies[0].Details)
	}
	if len(result.Connections[0].Paths) != 1 {
		t.Fatalf("expected one connection path, got %d", len(result.Connections[0].Paths))
	}
	if len(result.Connections[0].Paths[0].Steps) != 1 {
		t.Fatalf("expected one path step")
	}
	if op := result.Connections[0].Paths[0].Steps[0].Operation; op != "read" {
		t.Fatalf("unexpected path operation: %s", op)
	}

	// All six stages must have fired at least once. repo_facts has 1 call,
	// discovery has one per objective, detail has one per seed (2), connection
	// has one per exposure (1), reexamination is 0 because seeds were clean.
	fake.rec.mu.Lock()
	defer fake.rec.mu.Unlock()
	if fake.rec.roles["repo_facts"] != 1 {
		t.Fatalf("expected 1 repo_facts call, got %d", fake.rec.roles["repo_facts"])
	}
	if fake.rec.roles["discovery"] != 13 {
		t.Fatalf("expected 13 discovery calls (one per objective), got %d", fake.rec.roles["discovery"])
	}
	if fake.rec.roles["detail"] != 2 {
		t.Fatalf("expected 2 detail calls, got %d", fake.rec.roles["detail"])
	}
	if fake.rec.roles["connection"] != 1 {
		t.Fatalf("expected 1 connection call, got %d", fake.rec.roles["connection"])
	}
	if fake.rec.roles["reexamination"] != 0 {
		t.Fatalf("expected 0 reexamination calls when seeds are clean, got %d", fake.rec.roles["reexamination"])
	}
}

// ----------------------------------------------------------------------------
// Low-confidence handling via Stage 2 re-examination
// ----------------------------------------------------------------------------

type fakeLowConfidence struct {
	rec            *promptRecorder
	reject         bool // when true, re-exam returns empty items (rejection)
	detailKeepsLow bool // when true, the detail agent returns a low-confidence item
}

func (f *fakeLowConfidence) Enabled() bool { return true }
func (f *fakeLowConfidence) CreateSession(ctx context.Context, directory string) (string, error) {
	f.rec.observeSession(directory)
	return "s", nil
}
func (f *fakeLowConfidence) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeLowConfidence) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	payload, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(payload)
	return string(b), nil
}
func (f *fakeLowConfidence) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	f.rec.observeRole(role)
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /foo", "summary": "foo", "confidence": 0.2,
			"details":          map[string]any{"method": "GET", "path": "/foo"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1, "end_line": 2}},
		}}}, nil
	case role == "reexamination":
		if f.reject {
			return map[string]any{"items": []any{}}, nil
		}
		// Re-confirm the candidate with good confidence and details.
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /foo", "summary": "foo (re-verified)", "confidence": 0.9,
			"details":          map[string]any{"method": "GET", "path": "/foo"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1, "end_line": 2}},
			"evidence": []any{map[string]any{
				"file": "api.go", "start_line": 1, "end_line": 2,
				"snippet": "router.GET(\"/foo\", getFoo)", "source": "opencode",
			}},
		}}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		if f.detailKeepsLow {
			return map[string]any{"item": map[string]any{
				"type": "http_route", "name": "GET /foo", "summary": "foo (low)", "confidence": 0.2,
				"details":          map[string]any{"method": "GET", "path": "/foo"},
				"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1, "end_line": 2}},
			}}, nil
		}
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /foo", "summary": "foo (detailed)", "confidence": 0.92,
			"details":          map[string]any{"method": "GET", "path": "/foo", "auth": "none"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 1, "end_line": 2}},
			"evidence": []any{map[string]any{
				"file": "api.go", "start_line": 1, "end_line": 2,
				"snippet": "router.GET(\"/foo\", getFoo)", "source": "opencode",
			}},
		}}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

// When Stage 2 rescues a low-confidence seed, it should become a full exposure.
func TestRunReexaminationRescuesLowConfidenceSeed(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Quality.MinConfidence = 0.7
	fake := &fakeLowConfidence{rec: newRecorder(), reject: false}

	result, err := Run(context.Background(), cfg, t.TempDir(), fake)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := len(result.Exposures); got != 1 {
		t.Fatalf("expected 1 rescued exposure, got %d (unresolved=%d)", got, len(result.Unresolved))
	}
	if got := result.Exposures[0].Summary; !strings.Contains(got, "detailed") {
		t.Fatalf("expected rescued+detailed summary, got %q", got)
	}
	fake.rec.mu.Lock()
	if fake.rec.roles["reexamination"] != 1 {
		t.Errorf("expected 1 reexamination call, got %d", fake.rec.roles["reexamination"])
	}
	fake.rec.mu.Unlock()
}

// When Stage 2 rejects a suspect seed, it should appear as an unresolved item
// with reason_code=rejected_on_reexamination, and no exposure should be emitted.
func TestRunReexaminationRejectsSuspectSeed(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Quality.MinConfidence = 0.7
	fake := &fakeLowConfidence{rec: newRecorder(), reject: true}

	result, err := Run(context.Background(), cfg, t.TempDir(), fake)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := len(result.Exposures); got != 0 {
		t.Fatalf("expected 0 exposures after rejection, got %d", got)
	}
	found := false
	for _, u := range result.Unresolved {
		if u.ReasonCode == "rejected_on_reexamination" && u.Type == "http_route" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected rejected_on_reexamination unresolved item; got: %+v", result.Unresolved)
	}
}

// When re-examination is explicitly skipped, low-confidence seeds fall through
// to detail enrichment (where they continue to fail the toBase confidence
// gate), producing low_confidence unresolved items.
func TestRunSkipReexaminationProducesLowConfidenceUnresolved(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	fake := &fakeLowConfidence{rec: newRecorder(), reject: true, detailKeepsLow: true}

	result, err := Run(context.Background(), cfg, t.TempDir(), fake)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := len(result.Exposures); got != 0 {
		t.Fatalf("expected 0 exposures, got %d", got)
	}
	found := false
	for _, u := range result.Unresolved {
		if u.ReasonCode == "low_confidence" && u.Type == "http_route" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected low_confidence unresolved item when skipping reexam; got: %+v", result.Unresolved)
	}
	fake.rec.mu.Lock()
	if fake.rec.roles["reexamination"] != 0 {
		t.Errorf("expected 0 reexamination calls when SkipReexamination=true, got %d", fake.rec.roles["reexamination"])
	}
	fake.rec.mu.Unlock()
}

// ----------------------------------------------------------------------------
// Connection batching
// ----------------------------------------------------------------------------

type fakeBatching struct {
	rec            *promptRecorder
	mu             sync.Mutex
	connectionRuns int
}

func (f *fakeBatching) Enabled() bool { return true }
func (f *fakeBatching) CreateSession(ctx context.Context, directory string) (string, error) {
	f.rec.observeSession(directory)
	return "s", nil
}
func (f *fakeBatching) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeBatching) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	payload, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(payload)
	return string(b), nil
}
func (f *fakeBatching) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	f.rec.observeRole(role)
	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	depIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		depIDs[i] = util.StableID("dependency", "db_operation",
			"dep"+string(rune('0'+i)),
			"repo.go", // file
			func() string {
				// start:end matches the location emitted in discovery below.
				return map[int]string{0: "40:40", 1: "41:41", 2: "42:42", 3: "43:43", 4: "44:44"}[i]
			}(),
		)
	}
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "HTTP endpoint", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		items := make([]any, 0, 5)
		for i := 0; i < 5; i++ {
			items = append(items, map[string]any{
				"type": "db_operation", "name": "dep" + string(rune('0'+i)), "summary": "db op", "confidence": 0.95,
				"details":          map[string]any{"operation": "read", "table": "t" + string(rune('0'+i))},
				"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40 + i, "end_line": 40 + i}},
			})
		}
		return map[string]any{"items": items}, nil
	case role == "discovery":
		return map[string]any{"items": []any{}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "HTTP endpoint", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		for i := 0; i < 5; i++ {
			name := "dep" + string(rune('0'+i))
			if strings.Contains(prompt, "\"name\": \""+name+"\"") || strings.Contains(prompt, "\"name\":\""+name+"\"") {
				return map[string]any{"item": map[string]any{
					"type": "db_operation", "name": name, "summary": "db op", "confidence": 0.95,
					"details":          map[string]any{"operation": "read", "table": "t" + string(rune('0'+i))},
					"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40 + i, "end_line": 40 + i}},
				}}, nil
			}
		}
		return map[string]any{"item": nil}, nil
	case role == "detail":
		return map[string]any{"item": nil}, nil
	case role == "connection":
		f.mu.Lock()
		f.connectionRuns++
		f.mu.Unlock()
		items := make([]any, 0)
		for i := 0; i < 5; i++ {
			name := "dep" + string(rune('0'+i))
			if strings.Contains(prompt, "\"name\": \""+name+"\"") || strings.Contains(prompt, "\"name\":\""+name+"\"") {
				items = append(items, map[string]any{
					"from_exposure_id": exposureID,
					"to_dependency_id": depIDs[i],
					"summary":          "mapped",
					"confidence":       0.91,
					"path_signature":   "p" + string(rune('0'+i)),
					"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
				})
			}
		}
		return map[string]any{"items": items}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

// With MaxCatalogItems=2 and 5 deps, we expect ceil(5/2) = 3 connection
// batches, all 5 connections should survive.
func TestRunConnectionBatchingPreservesAllDependencies(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Runtime.MaxCatalogItems = 2
	cfg.Quality.MinConfidence = 0.7
	fake := &fakeBatching{rec: newRecorder()}

	result, err := Run(context.Background(), cfg, t.TempDir(), fake)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := len(result.Dependencies); got != 5 {
		t.Fatalf("expected 5 dependencies, got %d", got)
	}
	if got := len(result.Connections); got != 5 {
		t.Fatalf("expected 5 connections, got %d", got)
	}
	fake.mu.Lock()
	runs := fake.connectionRuns
	fake.mu.Unlock()
	if runs != 3 {
		t.Fatalf("expected 3 connection batches, got %d", runs)
	}
}

// ----------------------------------------------------------------------------
// Session reuse
// ----------------------------------------------------------------------------

func TestRunWithSharedSessionCreatesSingleSession(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Runtime.ReuseOpenCodeSession = true
	fake := newFakeOpenCode()

	if _, err := Run(context.Background(), cfg, t.TempDir(), fake); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	fake.rec.mu.Lock()
	calls := fake.rec.createCalls
	fake.rec.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 create session call with shared session, got %d", calls)
	}
}

// ----------------------------------------------------------------------------
// Closed-set to_dependency_id enforcement
// ----------------------------------------------------------------------------

type fakeOrphanConnection struct {
	rec *promptRecorder
}

func (f *fakeOrphanConnection) Enabled() bool { return true }
func (f *fakeOrphanConnection) CreateSession(ctx context.Context, directory string) (string, error) {
	f.rec.observeSession(directory)
	return "s", nil
}
func (f *fakeOrphanConnection) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeOrphanConnection) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	payload, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(payload)
	return string(b), nil
}
func (f *fakeOrphanConnection) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	f.rec.observeRole(role)
	exposureID := util.StableID("exposure", "http_route", "GET /users/{id}", "api.go", "10:30")
	dependencyID := util.StableID("dependency", "db_operation", "users_table_select", "repo.go", "40:60")

	switch {
	case role == "repo_facts":
		return map[string]any{}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"items": []any{map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}}, nil
	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"items": []any{map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"operation": "read", "table": "users"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}}, nil
	case role == "discovery":
		return map[string]any{"items": []any{}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}, nil
	case role == "detail" && strings.Contains(prompt, "OBJECTIVE_ID: dependency.db_operation"):
		return map[string]any{"item": map[string]any{
			"type": "db_operation", "name": "users_table_select", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"operation": "read", "table": "users"},
			"source_locations": []any{map[string]any{"file": "repo.go", "start_line": 40, "end_line": 60}},
		}}, nil
	case role == "connection":
		// Return TWO connections: one real, one referring to an unknown dep
		// ID that is not in the catalog.
		return map[string]any{"items": []any{
			map[string]any{
				"from_exposure_id": exposureID,
				"to_dependency_id": dependencyID,
				"summary":          "mapped",
				"confidence":       0.9,
				"path_signature":   "p",
				"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
			},
			map[string]any{
				"from_exposure_id": exposureID,
				"to_dependency_id": "FAKE_DEP_NOT_IN_CATALOG",
				"summary":          "should be dropped",
				"confidence":       0.9,
				"path_signature":   "p",
				"condition":        map[string]any{"kind": "predicate", "expression": "true", "explanation": "always"},
			},
		}}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

func TestRunDropsConnectionsWithOrphanDependencyIDs(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Quality.MinConfidence = 0.7
	fake := &fakeOrphanConnection{rec: newRecorder()}

	result, err := Run(context.Background(), cfg, t.TempDir(), fake)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if got := len(result.Connections); got != 1 {
		t.Fatalf("expected 1 surviving connection, got %d", got)
	}
}
