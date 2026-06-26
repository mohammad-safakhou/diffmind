package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
	"github.com/mohammad-safakhou/diffmind/internal/events"
)

// noopFake is a minimal openCodeAPI implementation that returns
// empty payloads instantly. We use it solely to exercise RunWith's
// pre-pipeline code path (snapshot creation, config sanitization,
// run_started emission) without actually running an LLM.
type noopFake struct {
	mu       sync.Mutex
	prompted bool
}

func (n *noopFake) Enabled() bool { return true }
func (n *noopFake) CreateSession(ctx context.Context, directory string) (string, error) {
	return "ses_test", nil
}
func (n *noopFake) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (n *noopFake) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	return "", nil
}
func (n *noopFake) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	n.mu.Lock()
	n.prompted = true
	n.mu.Unlock()
	role := discoverRole(prompt)
	switch role {
	case "repo_facts":
		return map[string]any{}, nil
	default:
		return map[string]any{"items": []any{}}, nil
	}
}

// REGRESSION (runs 20260518T113418Z and 20260518T115925Z): even when
// the caller hands the orchestrator a config with a sub-MaxCall
// transport timeout (the bad value that kept silently neutering the
// watchdog), the orchestrator MUST sanitize it upward before the
// pipeline talks to OpenCode. We exercise the path end-to-end by
// running a no-op pipeline and inspecting the run_started event for
// the EFFECTIVE timeout settings.
func TestOrchestratorSanitizesBadTransportTimeout(t *testing.T) {
	cfg := config.Default()
	cfg.OpenCode.BaseURL = "http://127.0.0.1:0" // never reachable; we use a fake
	cfg.OpenCode.TimeoutSec = 300               // the bad value
	cfg.Runtime.MaxCallSeconds = 1800
	cfg.Runtime.IdleTimeoutSec = 120
	cfg.Runtime.LivenessPollSec = 5

	// Use a sink that captures run_started so we can inspect the
	// effective timeout values the orchestrator advertises.
	captured := &captureSink{}
	tmp := t.TempDir()
	res, err := RunWith(context.Background(), cfg, tmp, &noopFake{}, RunOptions{
		Sink:  captured,
		RunID: "20260518T120000Z",
	})
	// We don't care if the run completed or failed (the noopFake
	// returns empty items so discovery yields 0 seeds; the pipeline
	// then exits cleanly). What we care about is that the
	// run_started event reflects the SANITIZED timeout.
	_ = res
	_ = err

	ev := captured.find("run_started")
	if ev == nil {
		t.Fatalf("no run_started event captured")
	}
	got, ok := ev.Payload["opencode_transport_timeout_sec"].(int)
	if !ok {
		// JSON-encoded floats are possible; normalise.
		switch v := ev.Payload["opencode_transport_timeout_sec"].(type) {
		case float64:
			got = int(v)
		default:
			t.Fatalf("opencode_transport_timeout_sec missing or wrong type: %T %v", v, ev.Payload["opencode_transport_timeout_sec"])
		}
	}
	if got < cfg.Runtime.MaxCallSeconds {
		t.Fatalf("transport timeout was NOT sanitized: %d < MaxCallSeconds %d", got, cfg.Runtime.MaxCallSeconds)
	}
	// And the run_started payload must include the watchdog values
	// so future debugging is one grep away.
	for _, field := range []string{
		"pipeline", "idle_timeout_sec", "max_call_sec", "liveness_poll_sec",
		"discovery_ast_hints", "discovery_verify", "discovery_verify_mode",
		"discovery_verify_samples", "discovery_framework_scope",
	} {
		if _, ok := ev.Payload[field]; !ok {
			t.Errorf("run_started.payload.%s missing — debugging future timeout regressions will be hard without it", field)
		}
	}
	// Cleanup retained snapshot.
	if res.SnapshotPath != "" {
		_ = os.RemoveAll(res.SnapshotPath)
	}
}

// captureSink stores every emitted event in memory so tests can
// assert against the payload after the run finishes. Concurrency-safe
// because the orchestrator may emit from multiple goroutines.
type captureSink struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	Kind    string         `json:"kind"`
	Stage   string         `json:"stage"`
	JobID   string         `json:"job_id"`
	Message string         `json:"message"`
	Payload map[string]any `json:"payload"`
}

func (c *captureSink) Emit(e events.Event) {
	// Roundtrip via JSON so we can inspect payload values as plain
	// map entries without re-importing event's internal shape.
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	var ce capturedEvent
	if err := json.Unmarshal(b, &ce); err != nil {
		return
	}
	c.mu.Lock()
	c.events = append(c.events, ce)
	c.mu.Unlock()
}

func (c *captureSink) find(kind string) *capturedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.events {
		if c.events[i].Kind == kind {
			return &c.events[i]
		}
	}
	return nil
}
