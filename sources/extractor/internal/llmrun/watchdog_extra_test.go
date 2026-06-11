package llmrun

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
	// parents maps a session id to its parentID. Used by the
	// LookupSessionParent stub. Set entries via setParent() for
	// subagent-style tests.
	parents map[string]string
	// parentLookups counts how often LookupSessionParent was hit
	// per session id, so tests can assert the cache prevents
	// re-polling for the same untracked session.
	parentLookups map[string]int
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

func (f *fakePauseAPI) LookupSessionParent(ctx context.Context, sessionID, directory string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.parentLookups == nil {
		f.parentLookups = map[string]int{}
	}
	f.parentLookups[sessionID]++
	if f.parents == nil {
		return "", nil
	}
	return f.parents[sessionID], nil
}

func (f *fakePauseAPI) setParent(child, parent string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.parents == nil {
		f.parents = map[string]string{}
	}
	f.parents[child] = parent
}

func (f *fakePauseAPI) lookupCount(sessionID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.parentLookups[sessionID]
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
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
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
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
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
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
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

// Reproducer for the dependency.outbound_http / queue_publish hang in
// run 20260521T112326Z. An OpenCode `task` tool spawns a subagent
// session, and the subagent raises an external_directory /tmp/*
// permission. The permission's SessionID is the subagent's, NOT the
// parent's, so the original `owns()` check rejected it as "not mine"
// and the run sat idle for 30 minutes until the hard ceiling fired.
// Fix: the watchdog must walk SessionState.ParentID up to one of its
// tracked sessions and treat the permission as ours when the chain
// roots in an owned session.
func TestWatchdogAutoDeniesPermissionFromSubagentOfTrackedSession(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("parent-s1")
	api.setParent("sub-1", "parent-s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	// The permission is raised by sub-1 (NOT tracked directly).
	// Mirror the production payload: external_directory pointing at
	// /tmp/* which the policy denies because the pattern doesn't
	// touch our snapshot.
	api.addPermission(PendingPermission{
		ID:        "p-sub",
		SessionID: "sub-1",
		Type:      "external_directory",
		Patterns:  []string{"/tmp/*"},
	})

	if !waitFor(t, 1*time.Second, func() bool {
		perms, _, _, _ := api.snapshot()
		return contains(perms, "p-sub")
	}) {
		t.Fatalf("expected sub-session permission to be denied via parent ownership")
	}
	// Must specifically be a DENY (the policy refuses /tmp/* writes).
	api.mu.Lock()
	resp := api.responses["p-sub"]
	api.mu.Unlock()
	if resp != "deny" {
		t.Fatalf("expected response=deny, got %q", resp)
	}
}

// Two-level subagent: parent -> sub-1 -> sub-2. The watchdog must
// recognise sub-2 as ours by following ParentID twice.
func TestWatchdogAutoDeniesPermissionFromGrandchildSubagent(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("parent-s1")
	api.setParent("sub-1", "parent-s1")
	api.setParent("sub-2", "sub-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{
		ID:        "p-deep",
		SessionID: "sub-2",
		Type:      "external_directory",
		Patterns:  []string{"/tmp/*"},
	})

	if !waitFor(t, 1*time.Second, func() bool {
		perms, _, _, _ := api.snapshot()
		return contains(perms, "p-deep")
	}) {
		t.Fatalf("expected grandchild subagent permission to be denied")
	}
}

// When the parent chain leads to a session that we do NOT own (some
// other client's session), the watchdog must NOT respond. This
// preserves the original "stay out of other clients' way" property.
func TestWatchdogIgnoresSubagentPermissionWhenChainRootIsForeign(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("parent-s1")
	api.setParent("foreign-sub", "FOREIGN-PARENT")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{
		ID:        "p-foreign",
		SessionID: "foreign-sub",
		Type:      "edit",
	})

	// Give it several ticks. The watchdog must NOT respond.
	time.Sleep(80 * time.Millisecond)
	perms, _, _, _ := api.snapshot()
	if contains(perms, "p-foreign") {
		t.Fatalf("watchdog must not reply to permissions whose parent chain is foreign")
	}
}

// The parent lookup is cached: re-polling the same unrecognised
// session over many ticks must hit the API at most once. This is
// important because the watchdog runs every 2s in production, and
// the OpenCode session DB sees no other writes — we don't want to
// repeatedly stress GET /session/{id} for sessions that will never
// resolve to one of ours.
func TestWatchdogCachesParentLookupForUntrackedSession(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 5*time.Millisecond)
	wd.Track("parent-s1")
	// foreign-sub's parent is "" (no parent), so the cache stores
	// a negative result on the first lookup.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{
		ID:        "p-noisy",
		SessionID: "foreign-sub",
		Type:      "edit",
	})

	// Let several ticks happen.
	time.Sleep(120 * time.Millisecond)
	if got := api.lookupCount("foreign-sub"); got != 1 {
		t.Fatalf("expected exactly 1 parent lookup for untracked session, got %d", got)
	}
}

// Permission directly from a tracked top-level session must still
// work without any parent lookup at all. This guards against
// regressing the common case while we add the subagent walk.
func TestWatchdogDoesNotLookUpParentForDirectlyOwnedSession(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 5*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{
		ID:        "p1",
		SessionID: "s1",
		Type:      "edit",
	})

	if !waitFor(t, 1*time.Second, func() bool {
		perms, _, _, _ := api.snapshot()
		return contains(perms, "p1")
	}) {
		t.Fatalf("expected directly-owned permission to be denied")
	}
	if got := api.lookupCount("s1"); got != 0 {
		t.Fatalf("expected NO parent lookups for directly owned session, got %d", got)
	}
}

// The same parent-walk logic must apply to clarification questions.
// In production OpenCode's @explore subagent can call the question
// tool too (e.g. "what file should I check next?"); without this
// branch the question would deadlock the parent's task tool just
// like the /tmp/* permission did.
func TestWatchdogAutoRejectsQuestionFromSubagentOfTrackedSession(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
	wd.Track("parent-s1")
	api.setParent("sub-1", "parent-s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addQuestion(PendingQuestion{ID: "q-sub", SessionID: "sub-1", Question: "which?"})

	if !waitFor(t, 1*time.Second, func() bool {
		_, qs, _, _ := api.snapshot()
		return contains(qs, "q-sub")
	}) {
		t.Fatalf("expected sub-session question to be rejected via parent ownership")
	}
}

func TestWatchdogStopIsIdempotent(t *testing.T) {
	api := &fakePauseAPI{}
	wd := NewWatchdog(api, "/snap", 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	wd.Stop()
	wd.Stop() // second call must not panic or hang
}

func TestWatchdogNilAPIIsNoop(t *testing.T) {
	wd := NewWatchdog(nil, "/snap", 10*time.Millisecond)
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
