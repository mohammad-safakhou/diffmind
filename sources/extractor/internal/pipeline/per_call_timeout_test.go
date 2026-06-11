package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mohammad-safakhou/diffmind/internal/config"
)

// fakePerCallTimeout simulates Go's net/http behaviour when an
// http.Client.Timeout fires: the error returned is a *url.Error
// wrapping context.DeadlineExceeded. Critically, the PARENT context
// is still alive — only the per-request transport context has been
// cancelled. The orchestrator's fail-fast filter must NOT treat this
// as collateral damage from a peer; it is itself the root cause.
type fakePerCallTimeout struct {
	mu                sync.Mutex
	httpRouteAttempts atomic.Int32
	otherAttempts     atomic.Int32
}

func (f *fakePerCallTimeout) Enabled() bool { return true }
func (f *fakePerCallTimeout) CreateSession(ctx context.Context, directory string) (string, error) {
	return "s", nil
}
func (f *fakePerCallTimeout) DeleteSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *fakePerCallTimeout) PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error) {
	m, err := f.PromptStructured(ctx, sessionID, directory, prompt, nil)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(m)
	return string(b), nil
}
func (f *fakePerCallTimeout) PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error) {
	role := discoverRole(prompt)
	switch {
	case role == "repo_facts":
		return map[string]any{}, nil

	case role == "discovery" && strings.Contains(prompt, "OBJECTIVE_ID: exposure.http_route"):
		f.httpRouteAttempts.Add(1)
		// Build the EXACT shape the standard library produces when
		// http.Client.Timeout fires: a *url.Error wrapping
		// context.DeadlineExceeded. The error message matches what
		// we observed in the real failed run.
		return nil, fmt.Errorf("discover.exposure.http_route prompt: %w", &url.Error{
			Op:  "Post",
			URL: "http://127.0.0.1:4096/session/foo/message?directory=bar",
			Err: context.DeadlineExceeded,
		})
	case role == "discovery":
		f.otherAttempts.Add(1)
		return map[string]any{"items": []any{}}, nil

	case role == "detail":
		return map[string]any{"item": nil}, nil

	case role == "connection":
		return map[string]any{"items": []any{}}, nil
	}
	return map[string]any{"items": []any{}}, nil
}

// REGRESSION TEST for the bug discovered in run 20260515T123031Z.
//
// Before the fix, errors.Is(err, context.DeadlineExceeded) returned
// TRUE for an http.Client.Timeout error, so the orchestrator filtered
// it out as if it were a peer-cancellation echo. The stage was then
// reported as `success` even though one (or more) objectives had
// failed, and the pipeline kept marching forward into reexamination
// and detail on incomplete data.
//
// The fix: workers explicitly flag PeerCancelled when they exit
// because the stage's child context was cancelled by a sibling;
// everything else — including per-call HTTP timeouts that wrap
// DeadlineExceeded — is treated as a root cause.
func TestPerCallHTTPTimeoutSurfacesAsDiscoveryFailure(t *testing.T) {
	cfg := config.Default()
	cfg.Runtime.Workers = 4
	cfg.Quality.MinConfidence = 0.7
	cfg.Runtime.SkipReexamination = true
	f := &fakePerCallTimeout{}

	res, err := Run(context.Background(), cfg, t.TempDir(), f)
	if err == nil {
		t.Fatalf("Run must fail when a discovery objective hits a per-call HTTP timeout")
	}
	if res.Failure == nil {
		t.Fatalf("Result.Failure must be populated")
	}
	if res.Failure.Stage != "discovery" {
		t.Fatalf("Failure.Stage = %q, want discovery (per-call timeout was silently swallowed?)", res.Failure.Stage)
	}
	if res.Failure.ObjectiveID != "exposure.http_route" {
		t.Errorf("Failure.ObjectiveID = %q, want exposure.http_route", res.Failure.ObjectiveID)
	}
	// The error classifier should label this as a timeout, not as
	// generic "cancelled" — the parent ctx is fine.
	if res.Failure.ErrorClass != "timeout" {
		t.Errorf("Failure.ErrorClass = %q, want timeout", res.Failure.ErrorClass)
	}
	// Cleanup retained snapshot.
	if res.SnapshotPath != "" {
		_ = os.RemoveAll(res.SnapshotPath)
	}
}
