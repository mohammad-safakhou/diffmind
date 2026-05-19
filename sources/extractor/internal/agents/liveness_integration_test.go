package agents

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
)

// stuckFake simulates the user-reported failure mode: OpenCode's
// session has STOPPED making progress mid-prompt. We satisfy:
//   - openCodeAPI: PromptStructured blocks until AbortSession is called.
//   - verbosePrompter (so the orchestrator wires the watchdog).
//   - livenessClient: GetSession/GetLatestMessage/ListPermissions
//     return frozen snapshots (no part growth, no permission pending,
//     no running tool).
//   - livenessAborter: AbortSession unblocks the prompt.
//
// We script ONE flaky objective (exposure.http_route) to freeze; all
// other objectives complete normally so the orchestrator only halts
// because of the watchdog, not collateral.
type stuckFake struct {
	stuckObjective string
	aborted        atomic.Bool
	abortSignal    chan struct{}
	abortOnce      sync.Once

	httpRouteAttempts atomic.Int32
}

func newStuckFake(stuckObjective string) *stuckFake {
	return &stuckFake{
		stuckObjective: stuckObjective,
		abortSignal:    make(chan struct{}),
	}
}

func (f *stuckFake) Enabled() bool { return true }
func (f *stuckFake) CreateSession(ctx context.Context, directory string) (string, error) {
	return "ses_test", nil
}
func (f *stuckFake) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}

func (f *stuckFake) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, _, _, err := f.PromptStructuredVerboseRaw(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}

func (f *stuckFake) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	m, _, _, err := f.PromptStructuredVerboseRaw(ctx, sessionID, directory, prompt, schema)
	return m, err
}

// PromptStructuredVerboseRaw blocks for the "stuck" objective until
// either the parent ctx OR the abort signal fires, mirroring what a
// real OpenCode would do when its model is producing no output. All
// other roles return a happy structured response.
func (f *stuckFake) PromptStructuredVerboseRaw(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, []byte, string, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil, "", nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: "+f.stuckObjective):
		f.httpRouteAttempts.Add(1)
		// Block until abort or ctx cancellation.
		select {
		case <-ctx.Done():
			return nil, nil, "", ctx.Err()
		case <-f.abortSignal:
			f.aborted.Store(true)
			return nil, nil, "", errors.New("prompt aborted by liveness watchdog")
		}

	case role == "discovery":
		return map[string]any{"items": []any{}}, nil, "", nil

	case role == "detail":
		return map[string]any{"item": nil}, nil, "", nil

	case role == "connection":
		return map[string]any{"items": []any{}}, nil, "", nil
	}
	return map[string]any{"items": []any{}}, nil, "", nil
}

// livenessClient surface: return frozen snapshots forever. The
// session header never advances; the latest message has one stable
// text part. No tool running. No permission pending.
func (f *stuckFake) GetSession(ctx context.Context, sessionID, directory string) (opencode.SessionState, error) {
	s := opencode.SessionState{ID: sessionID}
	s.Time.Updated = 1
	return s, nil
}
func (f *stuckFake) GetLatestMessage(ctx context.Context, sessionID, directory string) (opencode.Message, error) {
	m := opencode.Message{}
	m.Info.ID = "msg_test"
	t := struct {
		Start int64 `json:"start"`
		End   int64 `json:"end"`
	}{Start: 1}
	m.Parts = []opencode.MessagePart{{Type: "text", Text: "...", Time: &t}}
	return m, nil
}
func (f *stuckFake) ListPermissions(ctx context.Context, directory string) ([]opencode.PendingPermission, error) {
	return nil, nil
}

// livenessAborter surface: fire the abort signal so the in-flight
// PromptStructuredVerboseRaw returns. This is the real-world
// equivalent of POST /session/{id}/abort taking effect.
func (f *stuckFake) AbortSession(ctx context.Context, sessionID, directory string) error {
	f.abortOnce.Do(func() { close(f.abortSignal) })
	return nil
}

// Full end-to-end: a single discovery objective freezes mid-prompt
// (no part growth, no tool running, no permission pending). The
// orchestrator's liveness watchdog must:
//  1. Detect idleness within IdleTimeout + PollInterval.
//  2. Call AbortSession.
//  3. The aborted prompt returns; promptAgent re-labels the error
//     as a stuckError.
//  4. The orchestrator halts the discovery stage with
//     Failure.ErrorClass == "stuck".
func TestLivenessWatchdogAbortsStuckDiscoveryPrompt(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	// Tight watchdog parameters so the test runs fast: 80ms idle
	// window, 200ms poll. Well under the ceiling (defaults).
	cfg.Runtime.IdleTimeoutSec = 1  // 1 second
	cfg.Runtime.MaxCallSeconds = 60 // safety belt, not under test here
	cfg.Runtime.LivenessPollSec = 1 // 1 second
	// Override defaults via direct manipulation since these are int
	// fields with a 1-second floor. We'll override the watchdog
	// config more aggressively by setting tiny values directly on
	// the orchestrator below.
	_ = cfg

	f := newStuckFake("exposure.http_route")

	// Use Run rather than RunWith so we exercise the public surface,
	// but we need TINY watchdog timings; expose them via the integer
	// seconds knobs (minimum 1s each, so the test runs for ~2-3s).
	startedAt := time.Now()
	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	elapsed := time.Since(startedAt)

	if err == nil {
		t.Fatalf("expected hard failure when discovery prompt is stuck")
	}
	if res.Failure == nil {
		t.Fatalf("Result.Failure must be populated; got %+v", res)
	}
	if res.Failure.Stage != "discovery" {
		t.Fatalf("Failure.Stage = %q, want discovery", res.Failure.Stage)
	}
	if res.Failure.ErrorClass != "stuck" {
		t.Fatalf("Failure.ErrorClass = %q, want stuck (real error was: %s)", res.Failure.ErrorClass, res.Failure.Error)
	}
	if res.Failure.ObjectiveID != "exposure.http_route" {
		t.Errorf("Failure.ObjectiveID = %q, want exposure.http_route", res.Failure.ObjectiveID)
	}
	// The watchdog should have aborted within ~IdleTimeout +
	// PollInterval + small slack. Anything past ~10 seconds means
	// something is wrong with the integration.
	if elapsed > 10*time.Second {
		t.Errorf("run took %s; watchdog should have aborted in <10s", elapsed)
	}
	if !f.aborted.Load() {
		t.Errorf("AbortSession must have been called by the watchdog")
	}
	// Cleanup retained snapshot.
	if res.SnapshotPath != "" {
		_ = os.RemoveAll(res.SnapshotPath)
	}
}
