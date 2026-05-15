package agents

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

func TestClassifyError(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"connection reset by peer": "network",
		"connection refused":       "network",
		"i/o timeout":              "timeout",
		"Client.Timeout exceeded while awaiting headers":   "timeout",
		"context deadline exceeded":                        "timeout",
		"no such host":                                     "network",
		"prompt failed: 502 Bad Gateway":                   "http_5xx",
		"http: 503 Service Unavailable":                    "http_5xx",
		"prompt failed: 504 Gateway Timeout":               "http_5xx",
		"prompt failed: 401 Unauthorized: invalid api key": "http_4xx",
		"prompt failed: 404 Not Found":                     "http_4xx",
		"prompt failed: 429 Too Many Requests":             "rate_limit",
		"rate_limit reached for gpt-5":                     "rate_limit",
		"overloaded; please try again later":               "rate_limit",
		"no structured payload in response (raw=...)":      "schema",
		"json schema validation failed":                    "schema",
		"plain ol' weird error":                            "unknown",
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := classifyError(err); got != want {
			t.Errorf("classifyError(%q) = %q, want %q", msg, got, want)
		}
	}
}

func TestClassifyErrorRespectsContext(t *testing.T) {
	if classifyError(context.Canceled) != "cancelled" {
		t.Errorf("ctx.Canceled must classify as cancelled")
	}
	if classifyError(context.DeadlineExceeded) != "cancelled" {
		t.Errorf("ctx.DeadlineExceeded must classify as cancelled")
	}
}

type netTimeoutErr struct{}

func (netTimeoutErr) Error() string   { return "synthetic" }
func (netTimeoutErr) Timeout() bool   { return true }
func (netTimeoutErr) Temporary() bool { return true }

var _ net.Error = netTimeoutErr{}

func TestClassifyErrorRecognisesNetTimeout(t *testing.T) {
	if classifyError(netTimeoutErr{}) != "timeout" {
		t.Errorf("net.Error{Timeout:true} must classify as timeout")
	}
}

// On a hard pipeline failure both run_failure.json and run_failure.md
// must be written under runDir, and state/*.json must contain whatever
// stages did finish before the halt (here only repo_facts).
func TestFailureReportWrittenOnDiscoveryFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	runDir := filepath.Join(t.TempDir(), "20260101T000000Z")
	f := &fakeFlaky{failObjectiveID: "exposure.webhook"}
	res, err := RunWith(context.Background(), cfg, t.TempDir(), f, RunOptions{
		RunDir: runDir,
	})
	if err == nil {
		t.Fatalf("expected hard failure")
	}
	if res.Failure == nil {
		t.Fatalf("Result.Failure must be populated")
	}
	jsonPath := filepath.Join(runDir, "run_failure.json")
	mdPath := filepath.Join(runDir, "run_failure.md")
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("run_failure.json missing: %v", err)
	}
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("run_failure.md missing: %v", err)
	}
	body, _ := os.ReadFile(mdPath)
	bodyStr := string(body)
	for _, want := range []string{"# Run failure", "Stage", "How to retry", "diffmind retry"} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("run_failure.md missing %q; got:\n%s", want, bodyStr)
		}
	}
	// state/repo_facts.json should exist (Stage 0 succeeded).
	repoFactsState := filepath.Join(runDir, "state", "repo_facts.json")
	if _, err := os.Stat(repoFactsState); err != nil {
		t.Errorf("state/repo_facts.json missing: %v", err)
	}
	// state/discovery.json must NOT exist (Stage 1 halted before
	// completing the success boundary write).
	discoveryState := filepath.Join(runDir, "state", "discovery.json")
	if _, err := os.Stat(discoveryState); err == nil {
		t.Errorf("state/discovery.json must be absent when discovery halted; got %s", discoveryState)
	}
	// failure_state.json captures whatever was in flight.
	if _, err := os.Stat(filepath.Join(runDir, "state", "failure_state.json")); err != nil {
		t.Errorf("state/failure_state.json missing: %v", err)
	}
	// Cleanup retained snapshot.
	if res.SnapshotPath != "" {
		_ = os.RemoveAll(res.SnapshotPath)
	}
}
