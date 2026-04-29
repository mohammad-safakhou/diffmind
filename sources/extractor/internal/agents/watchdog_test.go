package agents

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakePauseAPI is a controllable pauseHandler used in watchdog tests.
type fakePauseAPI struct {
	mu                sync.Mutex
	pendingPerms      []PendingPermission
	pendingQuestions  []PendingQuestion
	respondedPerms    []string // permission IDs replied to
	rejectedQuestions []string
	abortedSessions   []string
	respondedDeny     int
	respondedAllow    int
	responses         map[string]string // permID -> last response
}

func (f *fakePauseAPI) AbortSession(ctx context.Context, sessionID, directory string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abortedSessions = append(f.abortedSessions, sessionID)
	return nil
}

func (f *fakePauseAPI) ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PendingPermission, len(f.pendingPerms))
	copy(out, f.pendingPerms)
	return out, nil
}

func (f *fakePauseAPI) RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respondedPerms = append(f.respondedPerms, permissionID)
	if f.responses == nil {
		f.responses = map[string]string{}
	}
	f.responses[permissionID] = response
	switch response {
	case "deny":
		f.respondedDeny++
	case "allow":
		f.respondedAllow++
	}
	// remove from pending so the watchdog doesn't keep replying.
	out := f.pendingPerms[:0]
	for _, p := range f.pendingPerms {
		if p.ID != permissionID {
			out = append(out, p)
		}
	}
	f.pendingPerms = out
	return nil
}

func (f *fakePauseAPI) ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PendingQuestion, len(f.pendingQuestions))
	copy(out, f.pendingQuestions)
	return out, nil
}

func (f *fakePauseAPI) RejectQuestion(ctx context.Context, requestID, directory string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rejectedQuestions = append(f.rejectedQuestions, requestID)
	out := f.pendingQuestions[:0]
	for _, q := range f.pendingQuestions {
		if q.ID != requestID {
			out = append(out, q)
		}
	}
	f.pendingQuestions = out
	return nil
}

func (f *fakePauseAPI) addPermission(p PendingPermission) {
	f.mu.Lock()
	f.pendingPerms = append(f.pendingPerms, p)
	f.mu.Unlock()
}

func (f *fakePauseAPI) addQuestion(q PendingQuestion) {
	f.mu.Lock()
	f.pendingQuestions = append(f.pendingQuestions, q)
	f.mu.Unlock()
}

func (f *fakePauseAPI) snapshot() (perms []string, qs []string, aborts []string, denies int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pc := append([]string{}, f.respondedPerms...)
	qc := append([]string{}, f.rejectedQuestions...)
	ac := append([]string{}, f.abortedSessions...)
	return pc, qc, ac, f.respondedDeny
}

func TestWatchdogAutoDeniesOwnedPermissions(t *testing.T) {
	api := &fakePauseAPI{}
	wd := newWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{ID: "p1", SessionID: "s1", Title: "edit file", Type: "edit"})

	if !waitFor(t, 1*time.Second, func() bool {
		perms, _, _, denies := api.snapshot()
		return contains(perms, "p1") && denies >= 1
	}) {
		t.Fatalf("expected permission p1 to be denied")
	}
}

func TestWatchdogIgnoresUntrackedSessionPermissions(t *testing.T) {
	api := &fakePauseAPI{}
	wd := newWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{ID: "p2", SessionID: "OTHER_CLIENT", Title: "x"})

	// Give the watchdog a couple of ticks to confirm it does NOT respond.
	time.Sleep(80 * time.Millisecond)
	perms, _, _, _ := api.snapshot()
	if contains(perms, "p2") {
		t.Fatalf("watchdog must not reply to permissions for sessions it does not own")
	}
}

func TestWatchdogAutoRejectsOwnedQuestions(t *testing.T) {
	api := &fakePauseAPI{}
	wd := newWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addQuestion(PendingQuestion{ID: "q1", SessionID: "s1", Question: "which one?"})

	if !waitFor(t, 1*time.Second, func() bool {
		_, qs, _, _ := api.snapshot()
		return contains(qs, "q1")
	}) {
		t.Fatalf("expected question q1 to be rejected")
	}
}

func TestWatchdogStopIsIdempotent(t *testing.T) {
	api := &fakePauseAPI{}
	wd := newWatchdog(api, "/snap", 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	wd.Stop()
	wd.Stop() // second call must not panic or hang
}

func TestWatchdogNilAPIIsNoop(t *testing.T) {
	wd := newWatchdog(nil, "/snap", 10*time.Millisecond)
	wd.Start(context.Background())
	wd.Track("s1")
	wd.Untrack("s1")
	wd.Stop()
}

// waitFor polls the predicate until it returns true or the deadline passes.
func waitFor(t *testing.T, d time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
