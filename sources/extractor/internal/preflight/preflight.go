// Package preflight runs the system-readiness checks DiffMind must
// pass before a run can be accepted. It is invoked in two places:
//
//  1. internal/ui/server.go, on a 30 s ticker, so the dashboard's
//     System Status panel reflects the current health of the host.
//  2. internal/app/run.go, synchronously, at the start of every run
//     (both fresh and retry). A single Severity == "fail" anywhere
//     in the Report aborts the run BEFORE we touch the snapshot or
//     allocate any resources.
//
// Every check is a self-contained Check implementation. New checks
// are added by appending to DefaultChecks().
//
// Design notes:
//   - Checks run in parallel but each is bounded by a per-check
//     timeout (5s default). A misbehaving check cannot stall the
//     dashboard.
//   - Severity is a tri-state. "warn" is informational and lets the
//     run proceed; only "fail" gates it. The user explicitly asked
//     for hard-rejection on Docker/OpenCode/credentials failures.
//   - Each Result carries a Remediation string so the UI can show
//     the user exactly what to do without forcing them to read code.
package preflight

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Severity is the tri-state result of a single check.
type Severity string

const (
	// SeverityOK means the check passed. Renders green.
	SeverityOK Severity = "ok"
	// SeverityWarn means the check passed in a degraded sense.
	// Examples: low disk space, no network reachable. The user
	// can still launch a run; some downstream behaviour may be
	// impaired but nothing will outright break. Renders amber.
	SeverityWarn Severity = "warn"
	// SeverityFail means a hard prerequisite is missing. The
	// dashboard disables the Run button and a CLI invocation
	// aborts before doing any work. Renders red.
	SeverityFail Severity = "fail"
)

// Result is the outcome of a single check. Fields are designed to
// render directly in the System Status panel without further
// transformation.
type Result struct {
	// Name is a short identifier ("docker", "opencode", ...).
	Name string `json:"name"`
	// Title is the human-readable label rendered in the UI.
	Title string `json:"title"`
	// Severity is ok/warn/fail.
	Severity Severity `json:"severity"`
	// Message is a one-sentence summary of the outcome.
	Message string `json:"message"`
	// Detail is the full reason. Long error strings, file paths,
	// exit codes, etc.
	Detail string `json:"detail,omitempty"`
	// Remediation is a human-actionable suggestion. Rendered as a
	// tooltip / expandable panel in the UI.
	Remediation string `json:"remediation,omitempty"`
	// DurationMs is how long the check ran. Useful in UI for
	// debugging slow checks.
	DurationMs int64 `json:"duration_ms"`
}

// Report is the aggregate of every check's Result plus the overall
// severity (the worst of the lot).
type Report struct {
	// Checks is the per-check breakdown, sorted by check name for
	// deterministic ordering.
	Checks []Result `json:"checks"`
	// Overall is the worst severity across Checks. "fail" if any
	// check failed; "warn" if any warned; "ok" otherwise.
	Overall Severity `json:"overall"`
	// GeneratedAt is the timestamp when the report was generated.
	GeneratedAt time.Time `json:"generated_at"`
}

// HasFail returns true when at least one check came back with
// Severity == "fail". The UI uses this to disable the Run button
// and app.Run uses it to reject the run synchronously.
func (r Report) HasFail() bool { return r.Overall == SeverityFail }

// Failures returns just the failed checks. Used by app.Run to build
// a clear error message listing every problem instead of just the
// first.
func (r Report) Failures() []Result {
	out := make([]Result, 0)
	for _, c := range r.Checks {
		if c.Severity == SeverityFail {
			out = append(out, c)
		}
	}
	return out
}

// Check is the interface each individual probe implements. Run is
// passed a context with the per-check timeout already applied.
type Check interface {
	// Name returns the stable identifier ("docker", "opencode"). It
	// MUST match the Result.Name produced by Run.
	Name() string
	// Title returns the human-readable label.
	Title() string
	// Run executes the probe. It must always return a populated
	// Result (never panic and never return a zero value).
	Run(ctx context.Context) Result
}

// Runner runs a fixed set of Checks in parallel with a per-check
// timeout. The same Runner instance is reused across invocations;
// callers do NOT share its internal state.
type Runner struct {
	checks  []Check
	timeout time.Duration
}

// NewRunner constructs a Runner. Pass DefaultChecks() in production;
// tests pass stubs.
func NewRunner(checks []Check) *Runner {
	return &Runner{checks: checks, timeout: 5 * time.Second}
}

// SetTimeout overrides the per-check timeout. Tests use this to
// keep slow probes from making the test suite flaky.
func (r *Runner) SetTimeout(d time.Duration) {
	if d <= 0 {
		return
	}
	r.timeout = d
}

// Run executes every registered check in parallel and aggregates the
// results into a Report. The returned Report.Checks slice is sorted
// by Name for deterministic UI ordering. The context is propagated
// to each check but each check additionally honours r.timeout.
func (r *Runner) Run(ctx context.Context) Report {
	now := time.Now().UTC()
	results := make([]Result, len(r.checks))

	var wg sync.WaitGroup
	for i, c := range r.checks {
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, r.timeout)
			defer cancel()
			started := time.Now()
			res := safeRun(cctx, c)
			res.DurationMs = time.Since(started).Milliseconds()
			// Defensive fill-in: any check that forgot to set Name
			// or Title gets them from the Check itself so the UI
			// never renders blank rows.
			if res.Name == "" {
				res.Name = c.Name()
			}
			if res.Title == "" {
				res.Title = c.Title()
			}
			results[i] = res
		}(i, c)
	}
	wg.Wait()

	sort.Slice(results, func(a, b int) bool { return results[a].Name < results[b].Name })

	return Report{
		Checks:      results,
		Overall:     overall(results),
		GeneratedAt: now,
	}
}

// safeRun wraps Check.Run in a panic recovery so a buggy probe
// can never take down the dashboard. A panic becomes a "fail" with
// the panic message in Detail.
func safeRun(ctx context.Context, c Check) (res Result) {
	defer func() {
		if rec := recover(); rec != nil {
			res = Result{
				Name:     c.Name(),
				Title:    c.Title(),
				Severity: SeverityFail,
				Message:  "check panicked",
				Detail:   recoverString(rec),
			}
		}
	}()
	return c.Run(ctx)
}

func recoverString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case error:
		return t.Error()
	default:
		return ""
	}
}

// overall returns the worst severity in results. Used by Run.
func overall(results []Result) Severity {
	worst := SeverityOK
	for _, r := range results {
		switch r.Severity {
		case SeverityFail:
			return SeverityFail // can't get worse
		case SeverityWarn:
			worst = SeverityWarn
		}
	}
	return worst
}
