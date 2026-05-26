package agents

import (
	"context"
	"testing"
	"time"
)

// snapshotDir is a realistic absolute path for tests; the watchdog uses
// only the basename to match patterns, so any unique tail works.
const snapshotDir = "/private/var/folders/c6/abc/T/diffmind-snap-22d5d5ce287e8f1c"

func TestDecidePermission_ReadIsAlwaysAllowed(t *testing.T) {
	d := decidePermission(PendingPermission{Permission: "read"}, snapshotDir)
	if d.Response != "allow" {
		t.Fatalf("read should allow, got %q (%s)", d.Response, d.Reason)
	}
}

func TestDecidePermission_GlobAndGrepAreAllowed(t *testing.T) {
	for _, kind := range []string{"glob", "grep", "webfetch"} {
		d := decidePermission(PendingPermission{Permission: kind}, snapshotDir)
		if d.Response != "allow" {
			t.Fatalf("%s should allow, got %q", kind, d.Response)
		}
	}
}

func TestDecidePermission_TaskIsDenied(t *testing.T) {
	d := decidePermission(PendingPermission{Permission: "task"}, snapshotDir)
	if d.Response != "deny" {
		t.Fatalf("task should deny, got %q (%s)", d.Response, d.Reason)
	}
}

func TestDecidePermission_ExternalDirectoryInsideSnapshotIsAllowed(t *testing.T) {
	// Even with a hallucinated parent path, as long as the asked pattern
	// contains the snapshot's basename, the agent is reading our sandbox.
	d := decidePermission(PendingPermission{
		Permission: "external_directory",
		Patterns:   []string{"/private/var/folders/c6/HALLUCINATED/T/diffmind-snap-22d5d5ce287e8f1c/src/main/*"},
	}, snapshotDir)
	if d.Response != "allow" {
		t.Fatalf("external_directory inside snapshot should allow, got %q (%s)", d.Response, d.Reason)
	}
}

func TestDecidePermission_ExternalDirectoryOutsideSnapshotIsDenied(t *testing.T) {
	d := decidePermission(PendingPermission{
		Permission: "external_directory",
		Patterns:   []string{"/Users/somebody/secrets/*"},
	}, snapshotDir)
	if d.Response != "deny" {
		t.Fatalf("external_directory outside snapshot should deny, got %q", d.Response)
	}
}

func TestDecidePermission_EditIsAlwaysDenied(t *testing.T) {
	d := decidePermission(PendingPermission{
		Permission: "edit",
		Patterns:   []string{snapshotDir + "/main.go"}, // even inside the snapshot
	}, snapshotDir)
	if d.Response != "deny" {
		t.Fatalf("edit should always deny, got %q", d.Response)
	}
}

func TestDecidePermission_BashIsDenied(t *testing.T) {
	for _, kind := range []string{"bash", "shell", "write", "patch"} {
		d := decidePermission(PendingPermission{Permission: kind}, snapshotDir)
		if d.Response != "deny" {
			t.Fatalf("%s should deny, got %q", kind, d.Response)
		}
	}
}

func TestDecidePermission_UnknownInsideSnapshotAllows(t *testing.T) {
	d := decidePermission(PendingPermission{
		Permission: "wholeNewKind",
		Patterns:   []string{snapshotDir + "/x"},
	}, snapshotDir)
	if d.Response != "allow" {
		t.Fatalf("unknown kind inside snapshot should allow (lean), got %q", d.Response)
	}
}

func TestDecidePermission_UnknownOutsideSnapshotDenies(t *testing.T) {
	d := decidePermission(PendingPermission{
		Permission: "wholeNewKind",
		Patterns:   []string{"/elsewhere/x"},
	}, snapshotDir)
	if d.Response != "deny" {
		t.Fatalf("unknown kind outside snapshot should deny, got %q", d.Response)
	}
}

func TestDecidePermission_PermissionFallsBackToType(t *testing.T) {
	// Older OpenCode releases don't fill Permission; only Type. We must
	// still use that to make a decision.
	d := decidePermission(PendingPermission{Type: "read"}, snapshotDir)
	if d.Response != "allow" {
		t.Fatalf("Type=read should still allow, got %q", d.Response)
	}
}

// Integration: the live watchdog must auto-allow an external_directory
// permission whose pattern is inside the snapshot, instead of denying it.
func TestWatchdogAllowsExternalDirectoryInSnapshot(t *testing.T) {
	api := &fakePauseAPI{}
	wd := newWatchdog(api, snapshotDir, 10*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	api.addPermission(PendingPermission{
		ID:         "p-extern",
		SessionID:  "s1",
		Permission: "external_directory",
		Patterns:   []string{"/private/var/folders/c6/HALLUC/T/diffmind-snap-22d5d5ce287e8f1c/src/*"},
	})

	if !waitFor(t, 1*time.Second, func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.responses["p-extern"] == "allow"
	}) {
		t.Fatalf("expected p-extern to be ALLOWED; responses=%v", api.responses)
	}
}

// Integration: a duplicate listing of an already-answered permission must
// not cause the watchdog to respond a second time. OpenCode keeps records
// in /permission for a few seconds after we reply, which previously caused
// the watchdog to send dozens of duplicate responses.
func TestWatchdogDoesNotRepeatResponses(t *testing.T) {
	api := &fakePauseAPI{}
	wd := newWatchdog(api, snapshotDir, 10*time.Millisecond)
	wd.Track("s1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wd.Start(ctx)
	defer wd.Stop()

	// Add a permission that fakePauseAPI removes upon response. Then
	// re-add it with the same id to simulate OpenCode still listing it.
	api.addPermission(PendingPermission{ID: "p-dup", SessionID: "s1", Permission: "edit"})
	if !waitFor(t, 1*time.Second, func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return len(api.respondedPerms) >= 1
	}) {
		t.Fatalf("first response did not happen")
	}
	api.addPermission(PendingPermission{ID: "p-dup", SessionID: "s1", Permission: "edit"})
	time.Sleep(80 * time.Millisecond)

	api.mu.Lock()
	count := 0
	for _, id := range api.respondedPerms {
		if id == "p-dup" {
			count++
		}
	}
	api.mu.Unlock()
	if count > 1 {
		t.Fatalf("watchdog responded %d times for the same permission; expected exactly 1", count)
	}
}
