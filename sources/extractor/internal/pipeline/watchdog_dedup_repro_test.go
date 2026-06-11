package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"
)

// stickyPauseAPI simulates the real OpenCode behavior: GET /permission keeps
// returning the SAME permission record even after we POST a deny — exactly
// what we observed in the live log. The watchdog's answered-map dedup must
// kick in or we end up emitting one auto_deny_permission event per tick.
type stickyPauseAPI struct {
	mu        sync.Mutex
	pending   []PendingPermission
	responses map[string]int
}

func (f *stickyPauseAPI) AbortSession(ctx context.Context, sessionID, directory string) error {
	return nil
}
func (f *stickyPauseAPI) ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PendingPermission, len(f.pending))
	copy(out, f.pending)
	return out, nil
}
func (f *stickyPauseAPI) RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.responses == nil {
		f.responses = map[string]int{}
	}
	f.responses[permissionID]++
	// NOTE: we do NOT remove from pending. This mirrors the real OpenCode
	// server bug where deny does not actually resolve the permission.
	return nil
}
func (f *stickyPauseAPI) ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error) {
	return nil, nil
}
func (f *stickyPauseAPI) RejectQuestion(ctx context.Context, requestID, directory string) error {
	return nil
}
func (f *stickyPauseAPI) LookupSessionParent(ctx context.Context, sessionID, directory string) (string, error) {
	return "", nil
}

func (f *stickyPauseAPI) addPermission(p PendingPermission) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = append(f.pending, p)
}

func (f *stickyPauseAPI) responseCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.responses[id]
}

// Reproducer for the live bug: when GET /permission keeps returning the
// same record, the watchdog must respond exactly once and then skip it on
// subsequent ticks.
func TestWatchdogDedupsWhenServerKeepsListingPermission(t *testing.T) {
	api := &stickyPauseAPI{}
	wd := newWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{
		ID:         "p-sticky",
		SessionID:  "s1",
		Permission: "external_directory",
		Patterns:   []string{"/elsewhere/*"},
	})

	// Wait for at least 5 ticks (50ms total) to elapse.
	time.Sleep(120 * time.Millisecond)

	if got := api.responseCount("p-sticky"); got != 1 {
		t.Fatalf("expected exactly 1 RespondPermission call for sticky permission, got %d", got)
	}
}
