package preflight

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubCheck is a minimal Check used by Runner tests. It returns
// whatever Result the test sets.
type stubCheck struct {
	name, title string
	result      Result
	delay       time.Duration
	panicWith   any
}

func (s *stubCheck) Name() string  { return s.name }
func (s *stubCheck) Title() string { return s.title }
func (s *stubCheck) Run(ctx context.Context) Result {
	if s.panicWith != nil {
		panic(s.panicWith)
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			// Reflect timeout in the result so the test can assert
			// the runner actually applied the per-check timeout.
			return Result{Name: s.name, Severity: SeverityFail, Message: "ctx: " + ctx.Err().Error()}
		}
	}
	return s.result
}

// TestRunnerAggregatesOverall checks that Runner.Run picks the
// worst severity across every check.
func TestRunnerAggregatesOverall(t *testing.T) {
	cases := []struct {
		name     string
		results  []Severity
		expected Severity
	}{
		{"all ok", []Severity{SeverityOK, SeverityOK}, SeverityOK},
		{"one warn", []Severity{SeverityOK, SeverityWarn}, SeverityWarn},
		{"one fail", []Severity{SeverityOK, SeverityFail, SeverityWarn}, SeverityFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stubs []Check
			for i, s := range c.results {
				stubs = append(stubs, &stubCheck{
					name:   "c" + string(rune('a'+i)),
					title:  "Check " + string(rune('A'+i)),
					result: Result{Severity: s},
				})
			}
			r := NewRunner(stubs)
			rep := r.Run(context.Background())
			if rep.Overall != c.expected {
				t.Errorf("Overall = %q, want %q", rep.Overall, c.expected)
			}
			if rep.HasFail() != (c.expected == SeverityFail) {
				t.Errorf("HasFail = %v, want %v", rep.HasFail(), c.expected == SeverityFail)
			}
		})
	}
}

// TestRunnerSortsCheckResultsByName guarantees deterministic UI
// ordering — the SystemStatus panel renders the slice in order
// without any further sorting.
func TestRunnerSortsCheckResultsByName(t *testing.T) {
	stubs := []Check{
		&stubCheck{name: "zeta", title: "Z", result: Result{Severity: SeverityOK}},
		&stubCheck{name: "alpha", title: "A", result: Result{Severity: SeverityOK}},
		&stubCheck{name: "mu", title: "M", result: Result{Severity: SeverityOK}},
	}
	r := NewRunner(stubs)
	rep := r.Run(context.Background())
	want := []string{"alpha", "mu", "zeta"}
	for i, c := range rep.Checks {
		if c.Name != want[i] {
			t.Errorf("Checks[%d].Name = %q, want %q", i, c.Name, want[i])
		}
	}
}

// TestRunnerRecoversFromPanic confirms a check that panics is
// rendered as Severity=fail rather than taking down the runner.
func TestRunnerRecoversFromPanic(t *testing.T) {
	stubs := []Check{
		&stubCheck{name: "ok", title: "OK", result: Result{Severity: SeverityOK}},
		&stubCheck{name: "panicky", title: "P", panicWith: "boom"},
	}
	r := NewRunner(stubs)
	rep := r.Run(context.Background())
	if rep.Overall != SeverityFail {
		t.Errorf("Overall = %q, want fail", rep.Overall)
	}
	var panicked Result
	for _, c := range rep.Checks {
		if c.Name == "panicky" {
			panicked = c
		}
	}
	if panicked.Severity != SeverityFail {
		t.Errorf("panicky check Severity = %q, want fail", panicked.Severity)
	}
	if panicked.Detail != "boom" {
		t.Errorf("panicky Detail = %q, want boom", panicked.Detail)
	}
}

// TestRunnerHonoursPerCheckTimeout ensures that one slow check
// cannot stall the whole report.
func TestRunnerHonoursPerCheckTimeout(t *testing.T) {
	stubs := []Check{
		&stubCheck{name: "fast", title: "Fast", result: Result{Severity: SeverityOK}},
		&stubCheck{name: "slow", title: "Slow", delay: 200 * time.Millisecond,
			result: Result{Severity: SeverityOK}},
	}
	r := NewRunner(stubs)
	r.SetTimeout(20 * time.Millisecond)
	started := time.Now()
	rep := r.Run(context.Background())
	elapsed := time.Since(started)
	if elapsed > 150*time.Millisecond {
		t.Errorf("expected Run to complete within ~50ms, got %v", elapsed)
	}
	// The slow check should have failed via context deadline.
	for _, c := range rep.Checks {
		if c.Name == "slow" && c.Severity != SeverityFail {
			t.Errorf("slow check Severity = %q, want fail", c.Severity)
		}
	}
}

// TestCredentialsCheckRejectsBlank validates the most basic gating
// behaviour the dashboard relies on: an empty provider or model
// produces SeverityFail.
func TestCredentialsCheckRejectsBlank(t *testing.T) {
	cases := []struct {
		name        string
		provider    string
		model       string
		wantSev     Severity
		wantInMsg   string
	}{
		{"both empty", "", "", SeverityFail, "provider_id"},
		{"only provider", "anthropic", "", SeverityFail, "model_id"},
		{"only model", "", "claude", SeverityFail, "provider_id"},
		{"ok", "anthropic", "claude-sonnet-4", SeverityOK, "Provider"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := NewCredentialsCheck(c.provider, c.model).Run(context.Background())
			if res.Severity != c.wantSev {
				t.Errorf("Severity = %q, want %q", res.Severity, c.wantSev)
			}
			if c.wantInMsg != "" && !contains(res.Message, c.wantInMsg) {
				t.Errorf("Message %q does not contain %q", res.Message, c.wantInMsg)
			}
		})
	}
}

// TestOpenCodeCheckHandlesMissingURL ensures an unconfigured URL
// yields warn (not fail) so the dashboard doesn't refuse to render
// before the user has filled the form.
func TestOpenCodeCheckHandlesMissingURL(t *testing.T) {
	res := NewOpenCodeCheck("", "", "").Run(context.Background())
	if res.Severity != SeverityWarn {
		t.Errorf("Severity = %q, want warn", res.Severity)
	}
}

// TestReportFailures returns only the failed checks.
func TestReportFailures(t *testing.T) {
	rep := Report{
		Checks: []Result{
			{Name: "ok", Severity: SeverityOK},
			{Name: "warn", Severity: SeverityWarn},
			{Name: "fail1", Severity: SeverityFail},
			{Name: "fail2", Severity: SeverityFail},
		},
		Overall: SeverityFail,
	}
	failures := rep.Failures()
	if len(failures) != 2 {
		t.Fatalf("Failures returned %d, want 2", len(failures))
	}
	if failures[0].Name != "fail1" || failures[1].Name != "fail2" {
		t.Errorf("Failures ordering wrong: %+v", failures)
	}
}

// contains is a tiny helper so the test reads naturally.
func contains(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// _ uses errors so the import is referenced if we expand later.
var _ = errors.New
