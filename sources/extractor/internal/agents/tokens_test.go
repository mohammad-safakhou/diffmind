package agents

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// stageFromJob is the mapping promptAgent role -> stage name. Every
// role string the orchestrator constructs must map to a stage name
// the SPA's reducer expects. Lock this down so a future rename in
// one place but not the other produces a test failure rather than a
// silent UI regression.
func TestStageFromJob_Mapping(t *testing.T) {
	cases := map[string]string{
		"repo_facts":                                   "repo_facts",
		"discover.exposure.http_route":                 "discovery",
		"discover.dependency.db_operation":             "discovery",
		"reexamine.exposure.http_route.GET-/users-id-": "reexamination",
		"detail.exposure.http_route.GET-/users-id-":    "detail",
		"connections.4f3ce0":                           "connections",
		"connections.4f3ce0.batch.2":                   "connections",
		"weird.unknown.verb":                           "other",
		"":                                             "other",
	}
	for jobID, want := range cases {
		if got := stageFromJob(jobID); got != want {
			t.Errorf("stageFromJob(%q) = %q, want %q", jobID, got, want)
		}
	}
}

// tokenBucket.add must be a faithful sum and tokenBucket.Total
// must NOT include cache_read/cache_write (those are billed
// separately and we surface them on their own line). Both are
// invariants the dashboard depends on.
func TestTokenBucket_AddAndTotal(t *testing.T) {
	var b tokenBucket
	b.add(sessionState{Input: 100, Output: 50, Reasoning: 10, CacheRead: 9000, CacheWrite: 200, Cost: 0.0012})
	b.add(sessionState{Input: 200, Output: 75, Reasoning: 20, CacheRead: 9000, CacheWrite: 0, Cost: 0.0018})
	if b.Calls != 2 {
		t.Errorf("Calls = %d, want 2", b.Calls)
	}
	if b.Input != 300 {
		t.Errorf("Input = %d, want 300", b.Input)
	}
	if b.Output != 125 {
		t.Errorf("Output = %d, want 125", b.Output)
	}
	if b.Reasoning != 30 {
		t.Errorf("Reasoning = %d, want 30", b.Reasoning)
	}
	if b.Total() != 455 {
		t.Errorf("Total() = %d, want 455 (input + output + reasoning ONLY)", b.Total())
	}
	if b.CacheRead != 18000 {
		t.Errorf("CacheRead = %d, want 18000", b.CacheRead)
	}
	if b.CacheWrite != 200 {
		t.Errorf("CacheWrite = %d, want 200", b.CacheWrite)
	}
	if want := 0.003; b.Cost != want {
		t.Errorf("Cost = %f, want %f", b.Cost, want)
	}
}

// fakeTokenFake combines a working openCodeAPI with a tokenReader
// that returns predictable session counters. Each discovery
// objective gets 1000 tokens; the repo_facts call gets 250.
// Detail / connections are zero (no items produced). The test then
// inspects llm_call_completed events and the run-level token total.
type fakeTokenFake struct {
	mu            sync.Mutex
	calls         atomic.Int32
	roleBySession map[string]string
}

func (f *fakeTokenFake) Enabled() bool { return true }
func (f *fakeTokenFake) CreateSession(ctx context.Context, directory string) (string, error) {
	// Each session id encodes the prompt role so the GetSession
	// adapter can return role-specific counters.
	n := f.calls.Add(1)
	return "ses_test_" + strings.Repeat("0", 0) + util.StableID("ses", string(rune(n))), nil
}
func (f *fakeTokenFake) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeTokenFake) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *fakeTokenFake) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	// Record the session->role mapping so GetSession can later
	// return role-specific token counts.
	f.mu.Lock()
	if f.roleBySession == nil {
		f.roleBySession = map[string]string{}
	}
	f.roleBySession[sessionID] = role
	f.mu.Unlock()
	switch role {
	case "repo_facts":
		return map[string]any{}, nil
	case "discovery":
		if strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route") {
			return map[string]any{"items": []any{map[string]any{
				"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
				"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
				"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
			}}}, nil
		}
		return map[string]any{"items": []any{}}, nil
	case "detail":
		return map[string]any{"item": map[string]any{
			"type": "http_route", "name": "GET /users/{id}", "summary": "x", "confidence": 0.95,
			"details":          map[string]any{"method": "GET", "path": "/users/{id}"},
			"source_locations": []any{map[string]any{"file": "api.go", "start_line": 10, "end_line": 30}},
		}}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

// GetSession (the tokenReader surface) returns role-specific token
// counts so we can assert per-stage aggregation works.
func (f *fakeTokenFake) GetSession(ctx context.Context, sessionID, directory string) (sessionState, error) {
	f.mu.Lock()
	role := f.roleBySession[sessionID]
	f.mu.Unlock()
	switch role {
	case "repo_facts":
		return sessionState{ID: sessionID, Input: 100, Output: 100, Reasoning: 50, Cost: 0.002}, nil
	case "discovery":
		return sessionState{ID: sessionID, Input: 500, Output: 400, Reasoning: 100, Cost: 0.01}, nil
	case "detail":
		return sessionState{ID: sessionID, Input: 700, Output: 250, Reasoning: 50, Cost: 0.015}, nil
	default:
		return sessionState{ID: sessionID, Input: 50, Output: 25, Reasoning: 0, Cost: 0.001}, nil
	}
}

// Per-call llm_call_completed events MUST carry a tokens object
// when a tokenReader is wired, so the SPA can render per-job cost
// without scraping the message bodies.
func TestPromptAgent_AttachesTokensToCompletionEvent(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	f := &fakeTokenFake{}

	sink := &captureSink{}
	tmp := t.TempDir()
	res, err := RunWith(context.Background(), cfg, tmp, f, RunOptions{
		Sink:  sink,
		RunID: "20260601T120000Z",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = res

	// Find at least one llm_call_completed event with a tokens map.
	found := 0
	for _, e := range sink.events {
		if e.Kind != "llm_call_completed" {
			continue
		}
		if t0, ok := e.Payload["tokens"].(map[string]any); ok {
			found++
			if total, ok := t0["total"].(float64); ok && total <= 0 {
				t.Errorf("llm_call_completed tokens.total = %f, want > 0", total)
			}
		}
	}
	if found == 0 {
		t.Fatalf("no llm_call_completed event carried a tokens payload (sink saw %d events)", len(sink.events))
	}
}

// run_completed must carry a per-stage token breakdown plus a
// "total" key with the run-wide aggregate. This is what the SPA's
// runMeta.tokens reads.
func TestRunCompleted_CarriesPerStageAndTotalTokens(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	f := &fakeTokenFake{}

	sink := &captureSink{}
	tmp := t.TempDir()
	res, err := RunWith(context.Background(), cfg, tmp, f, RunOptions{
		Sink:  sink,
		RunID: "20260601T120100Z",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = res

	final := sink.find("run_completed")
	if final == nil {
		// Empty-payload runs can show up as run_failed depending
		// on whether the assembler had anything to publish. Either
		// is fine — we just need tokens in the payload.
		final = sink.find("run_failed")
	}
	if final == nil {
		t.Fatalf("no terminal run_* event captured")
	}
	tokens, ok := final.Payload["tokens"].(map[string]any)
	if !ok {
		t.Fatalf("terminal event has no tokens map; payload keys: %v", payloadKeys(final.Payload))
	}
	if _, ok := tokens["total"]; !ok {
		t.Errorf("tokens map missing 'total' key (got keys %v)", payloadKeys(tokens))
	}
	// At least one stage-keyed entry should be present (repo_facts
	// or discovery, depending on what the fake actually ran).
	stageSeen := false
	for k := range tokens {
		if k == "total" {
			continue
		}
		stageSeen = true
		break
	}
	if !stageSeen {
		t.Errorf("tokens map only has 'total'; expected at least one per-stage entry")
	}

	// Result.Tokens should mirror what the event carries.
	if res.Tokens == nil {
		t.Errorf("Result.Tokens must be populated when terminal event has tokens")
	}
	if total, ok := res.Tokens["total"]; !ok || total.Total <= 0 {
		t.Errorf("Result.Tokens[total].Total must be > 0; got %+v", res.Tokens["total"])
	}
}

// payloadKeys returns the keys of a map[string]any sorted by go's
// default range order. Used in test failure messages only.
func payloadKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// When the orchestrator's tokenReader returns errors, the run MUST
// still succeed — token reads are diagnostic, not load-bearing.
type fakeTokenErrorFake struct {
	fakeTokenFake
}

func (f *fakeTokenErrorFake) GetSession(ctx context.Context, sessionID, directory string) (sessionState, error) {
	return sessionState{}, errAlwaysFail
}

var errAlwaysFail = newAlwaysFailErr()

type alwaysFailErr struct{}

func (alwaysFailErr) Error() string { return "synthetic token-read failure" }

func newAlwaysFailErr() error { return alwaysFailErr{} }

func TestPromptAgent_TolerateTokenReadFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 2
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	f := &fakeTokenErrorFake{}

	res, err := RunWith(context.Background(), cfg, t.TempDir(), f, RunOptions{
		RunID: "20260601T120200Z",
	})
	if err != nil {
		t.Fatalf("token read errors must not fail the run: %v", err)
	}
	// Result.Tokens may be nil when every read failed; that's fine.
	// Just make sure we got SOMETHING back.
	_ = res
}
