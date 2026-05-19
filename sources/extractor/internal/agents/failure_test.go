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
		"Client.Timeout exceeded while awaiting headers": "timeout",
		"context deadline exceeded":                      "timeout",
		"no such host":                                   "network",
		"prompt failed: 502 Bad Gateway":                 "http_5xx",
		"http: 503 Service Unavailable":                  "http_5xx",
		"prompt failed: 504 Gateway Timeout":             "http_5xx",
		// "invalid api key" matches the auth catalogue (more
		// actionable than http_4xx); the user knows to renew the
		// key rather than poking around at a generic 4xx.
		"prompt failed: 401 Unauthorized: invalid api key": "auth",
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

// REGRESSION: run 20260518T122739Z failed with an OAuth-token
// expiry that the original classifier labelled as `schema` because
// the provider wrapped it in a "no structured payload" envelope.
// The user got the wrong tip ("the model didn't honour the schema")
// when the real fix was "renew your token". The new classifier
// drills past the wrapper and surfaces auth/quota as their own
// actionable classes.
func TestClassifyError_AuthAndQuotaSurfaceThroughSchemaWrapper(t *testing.T) {
	cases := map[string]string{
		// Real string from run 20260518T122739Z, abbreviated.
		`detail.dependency.db_operation.X prompt: no structured payload in response (APIError: (Encountered invalidated oauth token for user, failing request)) raw={...}`: "auth",
		// Other common provider phrasings.
		`prompt: no structured payload in response (error: token has expired)`: "auth",
		`prompt failed: APIError invalid api key for org-foo`:                  "auth",
		`prompt: authentication failed for provider`:                           "auth",
		// Quota / billing — distinct from rate_limit.
		`{"error":{"code":"insufficient_quota","message":"You exceeded your current quota..."}}`: "quota",
		`prompt: no structured payload in response (You've reached your usage limit)`:            "quota",
		`prompt: error code billing required`:                                                    "quota",
	}
	for msg, want := range cases {
		if got := classifyError(errors.New(msg)); got != want {
			t.Errorf("classifyError(%q) = %q, want %q", msg, got, want)
		}
	}
}

// "rate_limit" must still win over "auth"/"quota" for the
// classic 429-shaped errors, because the actionable advice is
// different (back off vs. switch account).
func TestClassifyError_RateLimitWinsOver429Wording(t *testing.T) {
	// "429 Too Many Requests" is a rate-limit; it does NOT mention
	// auth/quota wording.
	if got := classifyError(errors.New("prompt failed: 429 Too Many Requests")); got != "rate_limit" {
		t.Errorf("rate_limit must win over numeric 429; got %q", got)
	}
}

func TestClassifyErrorRespectsContext(t *testing.T) {
	// Caller-initiated cancellation (Ctrl-C, SIGTERM, ctx.cancel) is
	// the unambiguous "cancelled" case.
	if got := classifyError(context.Canceled); got != "cancelled" {
		t.Errorf("ctx.Canceled = %q, want cancelled", got)
	}
	// DeadlineExceeded — bare or wrapped — represents some clock
	// running out. In practice this is almost always a per-call or
	// per-stage timeout (we don't set a parent deadline anywhere in
	// DiffMind), so "timeout" is the more useful classification.
	// Note: context.DeadlineExceeded implements net.Error with
	// Timeout()==true, which is what makes this classification
	// possible. See classifyError's docstring for the rationale.
	if got := classifyError(context.DeadlineExceeded); got != "timeout" {
		t.Errorf("ctx.DeadlineExceeded = %q, want timeout", got)
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
