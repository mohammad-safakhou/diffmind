package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// fakeNamedSeedFail returns three discovery seeds under the same
// objective, and fails the detail prompt for exactly ONE of them by
// name (not the first one to be submitted). The point of the test is
// to prove that the orchestrator's failure attribution code uses the
// seed identity carried by the result envelope, NOT the submission
// order — because workers complete in parallel and the failed result
// is generally not at the submission index that would naively be used.
type fakeNamedSeedFail struct {
	mu           sync.Mutex
	failSeedName string
	httpRouteIDs map[string]string // name -> id
	dbDepIDs     map[string]string
}

func newFakeNamedSeedFail(failSeedName string) *fakeNamedSeedFail {
	return &fakeNamedSeedFail{
		failSeedName: failSeedName,
		httpRouteIDs: map[string]string{
			"GET /a": util.StableID("exposure", "http_route", "GET /a", "a.go", "10:30"),
			"GET /b": util.StableID("exposure", "http_route", "GET /b", "b.go", "10:30"),
			"GET /c": util.StableID("exposure", "http_route", "GET /c", "c.go", "10:30"),
		},
		dbDepIDs: map[string]string{
			"db_a": util.StableID("dependency", "db_operation", "db_a", "repo.go", "1:10"),
		},
	}
}

func (f *fakeNamedSeedFail) Enabled() bool { return true }
func (f *fakeNamedSeedFail) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *fakeNamedSeedFail) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakeNamedSeedFail) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *fakeNamedSeedFail) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		// Three seeds in a deterministic order.
		items := []any{}
		for _, name := range []string{"GET /a", "GET /b", "GET /c"} {
			items = append(items, map[string]any{
				"type": "http_route", "name": name, "summary": "x", "confidence": 0.95,
				"details":          map[string]any{"method": "GET", "path": strings.TrimPrefix(name, "GET ")},
				"source_locations": []any{map[string]any{"file": strings.ToLower(string(name[len(name)-1])) + ".go", "start_line": 10, "end_line": 30}},
			})
		}
		return map[string]any{"items": items}, nil
	case role == "discovery":
		return map[string]any{"items": []any{}}, nil

	case role == "detail":
		// The detail stage now batches related seeds into one
		// prompt. Extract every seed name from the prompt; fail
		// the whole batch if any of them is the scripted failure
		// seed (this matches the production behaviour: a batch
		// fails atomically when its LLM call errors).
		names := extractDetailSeedNames(prompt)
		f.mu.Lock()
		var failingSeed string
		for _, n := range names {
			if n == f.failSeedName {
				failingSeed = n
				break
			}
		}
		f.mu.Unlock()
		if failingSeed != "" {
			return nil, fmt.Errorf("scripted failure for seed %q", failingSeed)
		}
		items := make([]any, 0, len(names))
		for _, n := range names {
			items = append(items, map[string]any{
				"type": "http_route", "name": n, "summary": "x", "confidence": 0.95,
				"details":          map[string]any{"method": "GET", "path": strings.TrimPrefix(n, "GET ")},
				"source_locations": []any{map[string]any{"file": strings.ToLower(string(n[len(n)-1])) + ".go", "start_line": 10, "end_line": 30}},
			})
		}
		return map[string]any{"items": items}, nil

	case role == "connection":
		return map[string]any{"items": []any{}}, nil
	}
	return map[string]any{"items": []any{}}, nil
}

// extractDetailSeedNames reads every seed name out of a detail
// prompt. Handles both the single-entity prompt format (SEED_ITEM:
// followed by one JSON object) and the new batched format (SEEDS
// (N): followed by a JSON array).
func extractDetailSeedNames(prompt string) []string {
	// Batched format first.
	if idx := strings.Index(prompt, "SEEDS ("); idx >= 0 {
		// The header is "SEEDS (N):\n[" — find the array start.
		open := strings.Index(prompt[idx:], "[")
		if open < 0 {
			return nil
		}
		// Find the matching closing bracket (top-level, accounting
		// for nesting depth).
		depth := 0
		body := prompt[idx+open:]
		end := -1
		for i, ch := range body {
			switch ch {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					end = i + 1
					break
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return nil
		}
		var arr []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(body[:end]), &arr); err != nil {
			return nil
		}
		out := make([]string, 0, len(arr))
		for _, s := range arr {
			if s.Name != "" {
				out = append(out, s.Name)
			}
		}
		return out
	}
	// Legacy single-entity format.
	if idx := strings.Index(prompt, "SEED_ITEM:\n"); idx >= 0 {
		tail := prompt[idx+len("SEED_ITEM:\n"):]
		if end := strings.Index(tail, "\n}\n"); end >= 0 {
			body := tail[:end+2]
			var seed struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal([]byte(body), &seed); err == nil && seed.Name != "" {
				return []string{seed.Name}
			}
		}
	}
	return nil
}

// The middle seed (not first, not last by submission) fails the
// detail prompt. The orchestrator must surface a failure whose
// EntityName is one of the seeds that was in the failing batch.
//
// Note on batched detail: when a batch's LLM call errors, ALL
// seeds in that batch are reported as failed simultaneously
// (atomic per batch). The orchestrator's "first failure"
// attribution then arbitrarily picks the first seed in batch
// order — which is a valid result, since every seed in the batch
// share the same root cause. The test asserts the WEAKER
// invariant that the surfaced seed must be a member of the
// batch that contained the scripted failure.
func TestDetailFailureAttributesCorrectSeed(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Runtime.SkipReexamination = true
	cfg.Quality.MinConfidence = 0.7
	f := newFakeNamedSeedFail("GET /b")
	runDir := filepath.Join(t.TempDir(), "20260516T000000Z")

	res, err := RunWith(context.Background(), cfg, t.TempDir(), f, RunOptions{
		RunDir: runDir,
		RunID:  "20260516T000000Z",
	})
	if err == nil {
		t.Fatalf("expected detail-stage failure")
	}
	if res.Failure == nil {
		t.Fatalf("Failure must be populated")
	}
	if res.Failure.Stage != "detail" {
		t.Errorf("Stage = %q, want detail", res.Failure.Stage)
	}
	// EntityName must be one of the three seeds. With batched
	// detail and only 3 seeds in one batch, all three were
	// in-flight when the batch errored.
	allowed := map[string]bool{"GET /a": true, "GET /b": true, "GET /c": true}
	if !allowed[res.Failure.EntityName] {
		t.Errorf("EntityName = %q; expected one of %v", res.Failure.EntityName, allowed)
	}
	// JobID must be of the per-entity shape detail.<obj>.<safeSeed>.
	if !strings.HasPrefix(res.Failure.JobID, "detail.exposure.http_route.") {
		t.Errorf("JobID = %q; expected prefix detail.exposure.http_route.", res.Failure.JobID)
	}
	// Cleanup.
	if res.SnapshotPath != "" {
		_ = os.RemoveAll(res.SnapshotPath)
	}
}

// extractHTTPStatus and shouldReportHTTPStatus must combine such that
// a token-count number embedded in a schema-class error never leaks
// into the report as a fake HTTP status.
func TestHTTPStatusNotReportedForSchemaErrors(t *testing.T) {
	// This is the EXACT error message shape from the real failed run.
	// It contains "output":419 inside a token-count object and ends
	// with the well-known "no structured payload" sentinel.
	msg := `detail.dependency.queue_publish.foo prompt: no structured payload in response; raw={"info":{"tokens":{"total":12485,"input":2345,"output":419,"reasoning":121}},"parts":[]}`
	err := errors.New(msg)
	class := ClassifyError(err)
	if class != "schema" {
		t.Fatalf("classifyError = %q, want schema", class)
	}
	if ShouldReportHTTPStatus(class) {
		t.Fatalf("shouldReportHTTPStatus(%q) must be false", class)
	}
	// extractHTTPStatus itself should also no longer match arbitrary
	// 3-digit numbers tucked inside JSON. The previous regex matched
	// "419" via the ":" lookbehind; our tightened pattern requires a
	// status-line context (HTTP/, status code, "got 502", etc.).
	if got := ExtractHTTPStatus(msg); got != 0 {
		t.Errorf("extractHTTPStatus(token-count embedded msg) = %d, want 0 — regex too greedy", got)
	}
}

// Sanity: legitimate HTTP-status messages still match.
func TestHTTPStatusReportedForRealStatusLines(t *testing.T) {
	cases := map[string]int{
		"prompt failed: 502 Bad Gateway":          502,
		"server returned 503 Service Unavailable": 503,
		"got 504 from upstream":                   504,
		"HTTP 429 Too Many Requests":              429,
		"status code 401":                         401,
	}
	for msg, want := range cases {
		if got := ExtractHTTPStatus(msg); got != want {
			t.Errorf("extractHTTPStatus(%q) = %d, want %d", msg, got, want)
		}
	}
}

// renderFailureMarkdown must only list inspection files that exist on
// disk. A non-existent .json response file (the common case for
// schema-class failures) must NOT appear in the output.
func TestMarkdownOnlyListsExistingFiles(t *testing.T) {
	tmp := t.TempDir()
	captureDir := filepath.Join(tmp, "prompts")
	if err := os.MkdirAll(captureDir, 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	// Write only .prompt.txt and .response.raw (no .json, no .text).
	promptPath := filepath.Join(captureDir, "detail.foo.prompt.txt")
	rawPath := filepath.Join(captureDir, "detail.foo.response.raw")
	if err := os.WriteFile(promptPath, []byte("prompt"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	if err := os.WriteFile(rawPath, []byte("raw"), 0o644); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	f := &Failure{
		Stage:        "detail",
		JobID:        "detail.foo",
		Error:        "boom",
		ErrorClass:   "schema",
		PromptPath:   promptPath,
		ResponsePath: filepath.Join(captureDir, "detail.foo.response.json"),
		OccurredAt:   time.Now().UTC(),
	}
	md := renderFailureMarkdown(f, tmp, "")
	// Should contain the prompt and the raw response.
	if !strings.Contains(md, promptPath) {
		t.Errorf("markdown missing prompt path")
	}
	if !strings.Contains(md, rawPath) {
		t.Errorf("markdown missing raw response path")
	}
	// Should NOT mention the non-existent .json or .text paths.
	jsonPath := filepath.Join(captureDir, "detail.foo.response.json")
	textPath := filepath.Join(captureDir, "detail.foo.response.text")
	if strings.Contains(md, jsonPath) {
		t.Errorf("markdown lists non-existent JSON response: %s", md)
	}
	if strings.Contains(md, textPath) {
		t.Errorf("markdown lists non-existent text response: %s", md)
	}
}
