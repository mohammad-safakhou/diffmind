package core

import (
	"context"
	"fmt"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/opencode"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// livenessConfig controls when the watchdog declares an in-flight
// prompt stuck and aborts it. All durations are positive; zero values
// fall back to defaults.
type LivenessConfig struct {
	IdleTimeout  time.Duration // declare stuck after this long with no progress
	MaxCall      time.Duration // hard ceiling on total call duration
	PollInterval time.Duration // how often we poll for progress
}

// livenessDefaults are the production-safe defaults: 2-minute idle
// window, 30-minute hard ceiling, 5-second poll. These ratios mean a
// stuck call is caught within IdleTimeout + PollInterval (~125s) and
// a runaway loop is bounded at MaxCall (~30min) regardless.
var livenessDefaults = LivenessConfig{
	IdleTimeout:  120 * time.Second,
	MaxCall:      30 * time.Minute,
	PollInterval: 5 * time.Second,
}

// applyDefaults fills in zero fields with the production defaults.
// We do NOT clamp to a minimum positive value so tests can pass tiny
// durations to drive the state machine quickly.
func (c LivenessConfig) applyDefaults() LivenessConfig {
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = livenessDefaults.IdleTimeout
	}
	if c.MaxCall <= 0 {
		c.MaxCall = livenessDefaults.MaxCall
	}
	if c.PollInterval <= 0 {
		c.PollInterval = livenessDefaults.PollInterval
	}
	return c
}

// probeSnapshot is everything one poll iteration needs to decide
// "still alive" vs "stuck". Wraps the OpenCode session header and the
// latest assistant message, plus a flag for "is there a permission
// pending on this session?" (the pause-handler watchdog already
// tracks this; we just need the boolean here).
type ProbeSnapshot struct {
	Session        opencode.SessionState // /session/{id}
	Latest         opencode.Message      // /session/{id}/message?limit=1
	PermissionWait bool                  // a permission is pending for this session
}

// livenessProbe is the minimal interface the watchdog needs from the
// outside world. The orchestrator wires up a real implementation
// that hits OpenCode; tests inject a deterministic fake.
type LivenessProbe interface {
	Snapshot(ctx context.Context) (ProbeSnapshot, error)
}

// aborter is the action the watchdog takes when it gives up. The
// orchestrator wires this to opencode.AbortSession; tests inject a
// record-only fake. Returning an error from abort never propagates
// back to the caller — we already decided to give up.
type Aborter interface {
	Abort(ctx context.Context) error
}

// livenessReport is the verdict the watchdog hands back to the
// orchestrator. Reason is the human-readable string we surface in
// the failure report (e.g. "no progress for 127s; last part: tool
// 'read' running 130s ago"). LastTool / LastTime are populated
// best-effort for the dashboard.
type LivenessReport struct {
	Reason   string
	LastTool string
	LastWhen time.Time
	Aborted  bool
}

// runLiveness drives the watchdog from the moment a prompt is in
// flight to the moment it either completes (caller cancels watchCtx)
// or the watchdog itself decides to abort. Returns nil if the call
// completed normally (no stuck detection); otherwise returns a
// non-nil report describing the abort.
//
// The function is intentionally synchronous so the orchestrator can
// `go runLiveness(...)` from one place and `<-done` to know the
// verdict. Callers MUST cancel watchCtx as soon as the prompt POST
// returns, regardless of whether it succeeded or failed; otherwise
// the watchdog will keep polling forever.
//
// Concurrency rules:
//   - This goroutine owns the "activity" state. No external
//     synchronisation is needed.
//   - Snapshot/Abort calls use a derived context with a short
//     per-poll timeout so a network blip never wedges the
//     watchdog itself.
//   - When watchCtx is cancelled, the function returns cleanly
//     without consulting the latest snapshot (no late-abort race).
func RunLiveness(watchCtx context.Context, cfg LivenessConfig, probe LivenessProbe, abort Aborter, role string, sink events.Sink) *LivenessReport {
	cfg = cfg.applyDefaults()
	started := time.Now()
	lastActivityAt := started
	var lastActivity int64
	var lastPartCount int
	var lastPartTime int64

	// State name we last emitted to the events bus. Only transitions
	// produce an event so the timeline stays readable across long
	// runs; emitting on every poll would create thousands of identical
	// "progress observed" entries.
	const stateProgress = "progress"
	const stateIdle = "idle"
	const stateToolRunning = "tool_running"
	const statePermPending = "permission_pending"
	const stateUnknown = ""
	prevState := stateUnknown

	pollTimer := time.NewTimer(cfg.PollInterval)
	defer pollTimer.Stop()

	emit := func(message string, payload map[string]any) {
		if sink == nil {
			return
		}
		if payload == nil {
			payload = map[string]any{}
		}
		payload["role"] = role
		sink.Emit(events.Event{
			Kind: events.KindLog, JobID: role,
			Message: message, Payload: payload,
		})
	}

	for {
		select {
		case <-watchCtx.Done():
			// Prompt returned (success or failure). Done.
			return nil
		case <-pollTimer.C:
			// Continue below.
		}

		// Hard ceiling first, regardless of activity: a runaway loop
		// can keep producing parts forever.
		if total := time.Since(started); total > cfg.MaxCall {
			reason := fmt.Sprintf("hard ceiling exceeded: %s > MaxCall %s", total.Round(time.Second), cfg.MaxCall)
			util.Warn("agents.liveness", "aborting prompt: hard ceiling", map[string]any{
				"role": role, "elapsed_sec": total.Seconds(), "max_call_sec": cfg.MaxCall.Seconds(),
			})
			doAbort(watchCtx, abort, role)
			return &LivenessReport{Reason: reason, Aborted: true}
		}

		// Idle ceiling next: even without a successful snapshot, if
		// the idle window has elapsed since the last observed
		// activity, we abort. This handles the case where OpenCode
		// itself becomes unreachable mid-call — we get no progress
		// info AND no way to confirm liveness, so the conservative
		// choice is to give up rather than wait forever.
		if idle := time.Since(lastActivityAt); idle > cfg.IdleTimeout {
			reason := fmt.Sprintf("no progress for %s (>= IdleTimeout %s)", idle.Round(time.Second), cfg.IdleTimeout)
			util.Warn("agents.liveness", "aborting prompt: idle", map[string]any{
				"role":         role,
				"idle_sec":     idle.Seconds(),
				"idle_max_sec": cfg.IdleTimeout.Seconds(),
			})
			doAbort(watchCtx, abort, role)
			return &LivenessReport{Reason: reason, LastWhen: lastActivityAt, Aborted: true}
		}

		// Take a snapshot. Use a short bounded context so a slow
		// OpenCode response doesn't wedge the watchdog.
		probeCtx, cancel := context.WithTimeout(watchCtx, cfg.PollInterval)
		snap, err := probe.Snapshot(probeCtx)
		cancel()
		if err != nil {
			// Treat snapshot errors as "no information" — don't
			// abort *here*, but also don't pretend progress
			// happened. The next iteration will re-check the
			// idle ceiling so we don't loop forever on a dead
			// server.
			util.Trace("agents.liveness", "probe error", map[string]any{"role": role, "error": err})
			pollTimer.Reset(cfg.PollInterval)
			continue
		}

		// Decide whether this snapshot represents progress vs. the
		// previous one.
		activity := snap.Session.Activity()
		partCount := len(snap.Latest.Parts)
		lastPartStart := latestPartStart(snap.Latest)

		progressed := activity > lastActivity ||
			partCount > lastPartCount ||
			lastPartStart > lastPartTime

		if progressed {
			lastActivity = activity
			lastPartCount = partCount
			lastPartTime = lastPartStart
			lastActivityAt = time.Now()
			// Emit ONLY when we transition from a non-progress
			// state to progress. Steady progress runs are
			// silent — the dashboard's job pill stays "running",
			// which is signal enough. This keeps the events
			// timeline readable on multi-minute runs (without
			// this guard we accumulated ~75% of all polls as
			// noisy log entries; see run 20260518T113418Z).
			if prevState != stateProgress {
				emit("liveness: progress observed", map[string]any{
					"activity":    activity,
					"part_count":  partCount,
					"last_part":   describeLatestPart(snap.Latest),
					"elapsed_sec": int(time.Since(started).Seconds()),
				})
				prevState = stateProgress
			}
			pollTimer.Reset(cfg.PollInterval)
			continue
		}

		// No new activity. A pending permission is still our problem to
		// resolve, so pause while the permission watchdog catches up. A
		// running tool is different: OpenCode exposes no inner progress for
		// tools like `task`, so treating "tool running" as permanent
		// activity can mask a dead subagent until MaxCall. The tool start was
		// already counted as progress above; if the tool remains the latest
		// unchanged part, the regular idle window must be allowed to trip.
		if snap.PermissionWait {
			// Pause the idle clock: refresh the timestamp so the
			// next iteration starts fresh once the permission
			// resolves.
			lastActivityAt = time.Now()
			if prevState != statePermPending {
				emit("liveness: permission pending, idle clock paused", nil)
				prevState = statePermPending
			}
			pollTimer.Reset(cfg.PollInterval)
			continue
		}
		idle := time.Since(lastActivityAt)
		if idle > cfg.IdleTimeout {
			lastDesc := describeLatestPart(snap.Latest)
			reason := fmt.Sprintf("no progress for %s (>= IdleTimeout %s); last part: %s",
				idle.Round(time.Second), cfg.IdleTimeout, lastDesc)
			util.Warn("agents.liveness", "aborting prompt: idle", map[string]any{
				"role":         role,
				"idle_sec":     idle.Seconds(),
				"idle_max_sec": cfg.IdleTimeout.Seconds(),
				"last_part":    lastDesc,
			})
			doAbort(watchCtx, abort, role)
			tool := ""
			if p, ok := latestPart(snap.Latest); ok && p.Type == "tool" {
				tool = p.Tool
			}
			return &LivenessReport{
				Reason:   reason,
				LastTool: tool,
				LastWhen: lastActivityAt,
				Aborted:  true,
			}
		}

		if latestToolRunning(snap.Latest) {
			if prevState != stateToolRunning {
				emit("liveness: tool running, waiting for progress", map[string]any{
					"idle_sec":     int(idle.Seconds()),
					"idle_max_sec": int(cfg.IdleTimeout.Seconds()),
					"last_part":    describeLatestPart(snap.Latest),
				})
				prevState = stateToolRunning
			}
			pollTimer.Reset(cfg.PollInterval)
			continue
		}

		// Idle — but not yet past the threshold. Emit ONCE on
		// entry to this state so the dashboard can show "idle for
		// 12s of 120s", but don't keep re-emitting every poll.
		if prevState != stateIdle {
			emit("liveness: idle, no progress yet", map[string]any{
				"idle_sec":     int(idle.Seconds()),
				"idle_max_sec": int(cfg.IdleTimeout.Seconds()),
				"last_part":    describeLatestPart(snap.Latest),
			})
			prevState = stateIdle
		}
		pollTimer.Reset(cfg.PollInterval)
	}
}

// doAbort calls the injected aborter under a short-lived context so
// a stuck server can't keep us hanging while we try to clean up.
// Errors from the abort itself are non-fatal: the prompt POST is
// going to surface the cancellation anyway and that's what the
// orchestrator hands back to the user.
func doAbort(ctx context.Context, a Aborter, role string) {
	if a == nil {
		return
	}
	abortCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := a.Abort(abortCtx); err != nil {
		util.Warn("agents.liveness", "abort call failed", map[string]any{"role": role, "error": err})
	}
}

// latestPart returns the very last part of the latest message, or
// false when the message has no parts.
func latestPart(m opencode.Message) (opencode.MessagePart, bool) {
	if len(m.Parts) == 0 {
		return opencode.MessagePart{}, false
	}
	return m.Parts[len(m.Parts)-1], true
}

// latestPartStart pulls the most recent timestamp out of the latest
// part, defaulting to 0 when none is available. Different part types
// store their timestamp in different fields (top-level Time for
// text/reasoning, State.Time for tool); we honour both.
func latestPartStart(m opencode.Message) int64 {
	p, ok := latestPart(m)
	if !ok {
		return 0
	}
	if p.Time != nil && p.Time.Start > 0 {
		return p.Time.Start
	}
	if p.State != nil && p.State.Time != nil && p.State.Time.Start > 0 {
		return p.State.Time.Start
	}
	return 0
}

// latestToolRunning reports whether the latest part is a tool call
// whose state is still "running". The tool start counts as progress,
// but an unchanged running tool is still subject to the idle timeout.
func latestToolRunning(m opencode.Message) bool {
	p, ok := latestPart(m)
	if !ok || p.Type != "tool" || p.State == nil {
		return false
	}
	return p.State.Status == "running"
}

// openCodeLivenessProbe adapts an *opencode.Client into the
// livenessProbe interface the watchdog consumes. The two backing
// calls (GetSession + GetLatestMessage) are tiny (~500B + ~4KB) and
// localhost-only, so running them every PollInterval seconds across
// a few in-flight workers is genuinely free.
//
// We also consult the orchestrator's permission watchdog (the one
// that auto-replies to OpenCode's permission/clarification prompts)
// via ListPermissions, so the liveness clock correctly pauses while
// the agent is blocked waiting on us.
type OpenCodeLivenessProbe struct {
	oc        LivenessClient
	sessionID string
	directory string
}

// NewOpenCodeLivenessProbe wires a LivenessClient to a session so the
// orchestrator (a different package) can build the probe without
// reaching into core's unexported fields.
func NewOpenCodeLivenessProbe(oc LivenessClient, sessionID, directory string) *OpenCodeLivenessProbe {
	return &OpenCodeLivenessProbe{oc: oc, sessionID: sessionID, directory: directory}
}

// livenessClient is the narrow subset of *opencode.Client the
// liveness probe needs. Declared as an interface so future
// implementations or mocks can swap in.
type LivenessClient interface {
	GetSession(ctx context.Context, sessionID, directory string) (opencode.SessionState, error)
	GetLatestMessage(ctx context.Context, sessionID, directory string) (opencode.Message, error)
	ListPermissions(ctx context.Context, directory string) ([]opencode.PendingPermission, error)
}

// Snapshot fetches the current session state, the latest assistant
// message, and the pending-permission flag in three parallel
// requests. Errors from any single sub-call are tolerated: we
// return whatever partial data we got. The watchdog upstream treats
// a fully-empty snapshot as "no info" but doesn't reset its idle
// clock, so partial failures degrade gracefully.
func (p *OpenCodeLivenessProbe) Snapshot(ctx context.Context) (ProbeSnapshot, error) {
	if p == nil || p.oc == nil || p.sessionID == "" {
		return ProbeSnapshot{}, nil
	}

	type sessRes struct {
		s   opencode.SessionState
		err error
	}
	type msgRes struct {
		m   opencode.Message
		err error
	}
	type permRes struct {
		pending bool
		err     error
	}

	sessCh := make(chan sessRes, 1)
	msgCh := make(chan msgRes, 1)
	permCh := make(chan permRes, 1)

	go func() {
		s, err := p.oc.GetSession(ctx, p.sessionID, p.directory)
		sessCh <- sessRes{s, err}
	}()
	go func() {
		m, err := p.oc.GetLatestMessage(ctx, p.sessionID, p.directory)
		msgCh <- msgRes{m, err}
	}()
	go func() {
		perms, err := p.oc.ListPermissions(ctx, p.directory)
		pending := false
		for _, pp := range perms {
			if pp.SessionID == p.sessionID {
				pending = true
				break
			}
		}
		permCh <- permRes{pending, err}
	}()

	sess := <-sessCh
	msg := <-msgCh
	perm := <-permCh
	var firstErr error
	for _, e := range []error{sess.err, msg.err, perm.err} {
		if e != nil && firstErr == nil {
			firstErr = e
		}
	}
	return ProbeSnapshot{
		Session:        sess.s,
		Latest:         msg.m,
		PermissionWait: perm.pending,
	}, firstErr
}

// openCodeAborter adapts an *opencode.Client into the aborter
// interface. AbortSession is best-effort (it's the same call we
// already make in promptAgent's defer-cleanup path).
type OpenCodeAborter struct {
	oc        LivenessAborter
	sessionID string
	directory string
}

// NewOpenCodeAborter wires a LivenessAborter to a session so the
// orchestrator (a different package) can build the aborter without
// reaching into core's unexported fields.
func NewOpenCodeAborter(oc LivenessAborter, sessionID, directory string) *OpenCodeAborter {
	return &OpenCodeAborter{oc: oc, sessionID: sessionID, directory: directory}
}

type LivenessAborter interface {
	AbortSession(ctx context.Context, sessionID, directory string) error
}

func (a *OpenCodeAborter) Abort(ctx context.Context) error {
	if a == nil || a.oc == nil || a.sessionID == "" {
		return nil
	}
	return a.oc.AbortSession(ctx, a.sessionID, a.directory)
}

// describeLatestPart returns a short, log-friendly summary of the
// most recent part. Used in liveness events and the abort reason
// string so the dashboard and failure report can show what the
// model was doing right before we gave up.
func describeLatestPart(m opencode.Message) string {
	p, ok := latestPart(m)
	if !ok {
		return "(no parts)"
	}
	switch p.Type {
	case "tool":
		status := "?"
		if p.State != nil {
			status = p.State.Status
		}
		title := ""
		if p.State != nil && p.State.Title != "" {
			title = " " + p.State.Title
		}
		return fmt.Sprintf("tool %s [%s]%s", p.Tool, status, title)
	case "text", "reasoning":
		preview := p.Text
		if len(preview) > 80 {
			preview = preview[:80] + "..."
		}
		return fmt.Sprintf("%s: %q", p.Type, preview)
	default:
		return p.Type
	}
}
