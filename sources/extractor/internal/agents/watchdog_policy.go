package agents

import (
	"path/filepath"
	"strings"
)

// permissionDecision captures what the watchdog decided to do with a
// pending permission and why. The "why" is exposed in the dashboard event
// so users can quickly understand the watchdog's behavior.
type permissionDecision struct {
	Response string // "allow" | "deny" | "" (skip — not enough signal)
	Reason   string
}

// readOnlyPermissions are tools that only read. We auto-allow these even
// when their patterns leak outside the snapshot dir, because a read can
// never harm the user's repo (it points at the temp snapshot only — the
// agent literally cannot reach the user's filesystem from there).
var readOnlyPermissions = map[string]struct{}{
	"read":     {},
	"glob":     {},
	"grep":     {},
	"webfetch": {}, // network-only, doesn't touch disk
}

// deniedPermissions are non-mutating in the filesystem sense, but they are
// unsafe for DiffMind's headless pipeline. `task` delegates to a subagent
// session whose progress is opaque to the parent and has repeatedly wedged
// discovery until the hard ceiling. The parent prompt can do the same work
// with direct read/glob/grep tools, so deny delegation.
var deniedPermissions = map[string]struct{}{
	"task": {},
}

// mutatingPermissions are tools we never want to allow. These are the
// real reason the watchdog exists.
var mutatingPermissions = map[string]struct{}{
	"edit":  {},
	"write": {},
	"bash":  {},
	"shell": {},
	"patch": {},
}

// decidePermission decides what to do with a pending permission. The
// decision is based on the permission kind and any patterns the agent
// supplied. The snapshot's basename is used as the "this is OUR sandbox"
// signal: any path containing the snapshot directory name (even with a
// hallucinated parent path) is considered safe to allow.
//
// Rules:
//
//   - If permission kind is read-only ("read", "glob", "grep",
//     "webfetch"), allow regardless of pattern. Read tools cannot mutate
//     anything.
//   - If permission kind is denied ("task"), deny because delegation can
//     wedge headless runs even though it is not a filesystem mutation.
//   - If permission kind is mutating ("edit", "write", "bash", ...) AND
//     no pattern matches our snapshot, deny.
//   - If permission kind is "external_directory" and at least one
//     pattern contains our snapshot basename, allow. The macOS LLM
//     hallucination of /private/var/folders/.../diffmind-snap-<id>/...
//     ends up here.
//   - Otherwise (unknown kind, no patterns, or patterns clearly outside
//     the snapshot), deny.
func decidePermission(p PendingPermission, snapshotPath string) permissionDecision {
	kind := strings.ToLower(strings.TrimSpace(p.Permission))
	if kind == "" {
		kind = strings.ToLower(strings.TrimSpace(p.Type))
	}
	snapshotBase := filepath.Base(snapshotPath)
	matchesSnapshot := patternsTouchSnapshot(p.Patterns, snapshotBase)

	if _, ok := readOnlyPermissions[kind]; ok {
		return permissionDecision{
			Response: "allow",
			Reason:   "read-only permission (" + kind + "); always allowed",
		}
	}
	if _, denied := deniedPermissions[kind]; denied {
		return permissionDecision{
			Response: "deny",
			Reason:   "delegating tool (" + kind + ") disabled for headless extraction",
		}
	}
	if kind == "external_directory" {
		// OpenCode treats anything outside its bound working directory as
		// "external". The LLM frequently emits absolute paths under
		// /private/var/folders/.../diffmind-snap-<id>/... that look
		// external because of macOS path-canonicalization quirks. As long
		// as the asked path contains our snapshot basename, the agent is
		// asking for files inside our sandbox — allow.
		if matchesSnapshot {
			return permissionDecision{
				Response: "allow",
				Reason:   "external_directory inside our snapshot",
			}
		}
		return permissionDecision{
			Response: "deny",
			Reason:   "external_directory outside our snapshot",
		}
	}
	if _, mut := mutatingPermissions[kind]; mut {
		return permissionDecision{
			Response: "deny",
			Reason:   "mutating tool (" + kind + ") — read-only run",
		}
	}
	if matchesSnapshot {
		// Unknown kind but at least scoped to our sandbox; lean allow
		// rather than block the run.
		return permissionDecision{
			Response: "allow",
			Reason:   "unknown kind (" + kind + ") inside snapshot; defaulting to allow",
		}
	}
	return permissionDecision{
		Response: "deny",
		Reason:   "unknown kind (" + kind + ") with no snapshot match; defaulting to deny",
	}
}

// patternsTouchSnapshot returns true if any of the supplied patterns
// references the snapshot directory by basename. We deliberately match on
// basename rather than full path because the LLM occasionally produces
// path strings with a corrupted parent (e.g. /private/var/folders/c6/603r/
// instead of /private/var/folders/c6/603t/) — the basename is the stable
// part and that's what we sandbox on.
func patternsTouchSnapshot(patterns []string, snapshotBase string) bool {
	if snapshotBase == "" {
		return false
	}
	for _, p := range patterns {
		if strings.Contains(p, snapshotBase) {
			return true
		}
	}
	return false
}
