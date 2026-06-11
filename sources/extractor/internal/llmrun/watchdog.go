package llmrun

import (
	"context"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

// watchdog polls the OpenCode server for outstanding permission requests
// and clarification questions issued by sessions this orchestrator owns,
// and auto-replies so that prompts cannot deadlock waiting for human input.
//
// Why this exists:
//   - We run headless on a server. Nobody is at the TUI to click "allow".
//   - Our prompts already instruct the agent not to edit / not to ask
//     questions. A permission/question event therefore means the agent is
//     either misbehaving or hitting an unexpected tool path. In either case
//     the safe answer for our use case is "deny" / "reject", which lets
//     the prompt continue (or fail fast) rather than hang.
//   - Without this, a single rogue agent could block all 16 workers because
//     they would all hit the per-call timeout while sessions stay paused
//     server-side.
type Watchdog struct {
	api           PauseHandler
	directory     string
	pollInterval  time.Duration
	sink          events.Sink
	mu            sync.Mutex
	ownedSessions map[string]struct{}
	answered      map[string]struct{} // permissions we have already responded to
	// parentCache memoises the result of LookupSessionParent for
	// session ids that are NOT directly owned, so the watchdog spends
	// at most one round-trip per unrecognised session over the whole
	// run. Entries are written even for sessions we end up NOT
	// claiming (parent unknown / API error): empty string means "we
	// looked and it isn't ours", which is just as actionable as
	// "looked it up and it IS ours" — both cases need to be cached
	// so we don't re-poll every 2s.
	parentCache map[string]string
	stopCh      chan struct{}
	doneCh      chan struct{}
	stopOnce    sync.Once
}

func (w *Watchdog) markAnswered(permID string) {
	if w == nil || permID == "" {
		return
	}
	w.mu.Lock()
	if w.answered == nil {
		w.answered = map[string]struct{}{}
	}
	w.answered[permID] = struct{}{}
	w.mu.Unlock()
}

func (w *Watchdog) hasAnswered(permID string) bool {
	if w == nil || permID == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.answered[permID]
	return ok
}

func (w *Watchdog) unmarkAnswered(permID string) {
	if w == nil || permID == "" {
		return
	}
	w.mu.Lock()
	delete(w.answered, permID)
	w.mu.Unlock()
}

func NewWatchdog(api PauseHandler, directory string, poll time.Duration) *Watchdog {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	return &Watchdog{
		api:           api,
		directory:     directory,
		pollInterval:  poll,
		ownedSessions: map[string]struct{}{},
		answered:      map[string]struct{}{},
		parentCache:   map[string]string{},
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// SetSink wires a live event sink so watchdog actions are observable from
// the dashboard.
func (w *Watchdog) SetSink(s events.Sink) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.sink = s
	w.mu.Unlock()
}

func (w *Watchdog) emit(e events.Event) {
	if w == nil {
		return
	}
	w.mu.Lock()
	s := w.sink
	w.mu.Unlock()
	if s != nil {
		s.Emit(e)
	}
}

// Track records that a session id belongs to this orchestrator. Only
// permissions/questions from tracked sessions are auto-replied; this keeps
// us from interfering with other clients sharing the same OpenCode server.
func (w *Watchdog) Track(sessionID string) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	w.ownedSessions[sessionID] = struct{}{}
	w.mu.Unlock()
}

// Untrack removes a session id (e.g. after DeleteSession). It is safe to
// call multiple times.
func (w *Watchdog) Untrack(sessionID string) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	delete(w.ownedSessions, sessionID)
	w.mu.Unlock()
}

func (w *Watchdog) owns(sessionID string) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.ownedSessions[sessionID]
	return ok
}

// cachedParent returns the cached parent id for a session and a flag
// indicating whether we've ever looked it up. The flag distinguishes
// "we haven't checked yet" from "we checked and there is no parent
// (or the lookup failed)". The watchdog uses both states to avoid
// re-polling /session/{id} on every tick for sessions that turned
// out not to be ours.
func (w *Watchdog) cachedParent(sessionID string) (string, bool) {
	if w == nil || sessionID == "" {
		return "", false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	parent, ok := w.parentCache[sessionID]
	return parent, ok
}

// rememberParent records the looked-up parent for a session. We
// cache even the negative result ("" parent) so we don't repeatedly
// hit the server for the same untracked session.
func (w *Watchdog) rememberParent(sessionID, parentID string) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.parentCache == nil {
		w.parentCache = map[string]string{}
	}
	w.parentCache[sessionID] = parentID
}

// ownsTransitive returns true when sessionID is either directly owned
// (the common case for our top-level prompts) OR is a subagent whose
// parent chain leads to one of our owned sessions. The lookup walks
// at most depth subagents deep, using the cache to keep the cost at
// O(unique sessions seen) for the whole run.
//
// This exists because OpenCode's `task` tool creates an entirely
// separate session for each subagent, and any permission/question
// raised inside the subagent carries the subagent's id — not the
// parent's. If we ignore subagent permissions the parent's `task`
// tool sits in state=running forever; see the watchdog comment at
// the top of this file for the broader rationale.
//
// We bound the recursion at depth=4 as a safety net against
// pathological cycles in the parentID chain (the API hasn't shown
// any, but cycles in a remote-driven structure aren't worth
// trusting). depth=4 is plenty: a subagent calling task to spawn
// another subagent is rare; calling it 4 levels deep is implausible
// and a strong sign something has gone wrong upstream.
func (w *Watchdog) ownsTransitive(ctx context.Context, sessionID string) bool {
	if w == nil || sessionID == "" {
		return false
	}
	if w.owns(sessionID) {
		return true
	}
	seen := map[string]struct{}{sessionID: {}}
	current := sessionID
	for depth := 0; depth < 4; depth++ {
		parent, cached := w.cachedParent(current)
		if !cached {
			lookCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			p, err := w.api.LookupSessionParent(lookCtx, current, w.directory)
			cancel()
			if err != nil {
				util.Trace("agents.watchdog", "lookup parent failed", map[string]any{
					"session_id": current, "error": err.Error(),
				})
				// Cache the empty result so we don't re-poll on
				// every tick for this session. If it really IS
				// ours, the worst case is one missed permission
				// — the next subagent the same parent spawns
				// will produce a fresh session id and we'll try
				// again.
				w.rememberParent(current, "")
				return false
			}
			w.rememberParent(current, p)
			parent = p
		}
		if parent == "" {
			return false
		}
		if w.owns(parent) {
			return true
		}
		if _, loop := seen[parent]; loop {
			util.Trace("agents.watchdog", "parent chain cycle", map[string]any{
				"session_id": sessionID, "loop_at": parent,
			})
			return false
		}
		seen[parent] = struct{}{}
		current = parent
	}
	return false
}

// Start launches the polling goroutine. It is a no-op if api is nil.
func (w *Watchdog) Start(ctx context.Context) {
	if w == nil || w.api == nil {
		if w != nil {
			close(w.doneCh)
		}
		return
	}
	go w.loop(ctx)
}

// Stop signals the watchdog to exit and waits for the goroutine to finish.
func (w *Watchdog) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.doneCh
}

func (w *Watchdog) loop(ctx context.Context) {
	defer close(w.doneCh)
	t := time.NewTicker(w.pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *Watchdog) tick(ctx context.Context) {
	// Use a short, independent timeout for each poll so a slow server can't
	// stall the watchdog itself.
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if perms, err := w.api.ListPermissions(pollCtx, w.directory); err == nil {
		for _, p := range perms {
			if !w.ownsTransitive(pollCtx, p.SessionID) {
				continue
			}
			// Skip permissions we already answered. OpenCode keeps them
			// in GET /permission for several seconds (and sometimes
			// forever, when our deny doesn't actually resolve the
			// permission server-side) — without this dedup we'd emit one
			// auto-allow / auto-deny event per tick. The check uses
			// hasAnswered() under the lock to avoid any chance of stale
			// reads.
			if w.hasAnswered(p.ID) {
				continue
			}
			decision := DecidePermission(p, w.directory)
			if decision.Response == "" {
				continue
			}
			// Reserve the id BEFORE the network call so a slow round-trip
			// can't cause a second tick to enter this block. If the call
			// fails we roll back.
			w.markAnswered(p.ID)

			level := "auto-allowing"
			if decision.Response == "deny" {
				level = "auto-denying"
			}
			util.Warn("agents.watchdog", level+" permission", map[string]any{
				"session_id":    p.SessionID,
				"permission_id": p.ID,
				"kind":          p.Permission,
				"type":          p.Type,
				"patterns":      p.Patterns,
				"reason":        decision.Reason,
			})
			w.emit(events.Event{
				Kind:   events.KindWatchdogAction,
				JobID:  "watchdog.permission",
				Status: decision.Response,
				Payload: map[string]any{
					"action":        "auto_" + decision.Response + "_permission",
					"session_id":    p.SessionID,
					"permission_id": p.ID,
					"kind":          p.Permission,
					"type":          p.Type,
					"patterns":      p.Patterns,
					"reason":        decision.Reason,
				},
			})
			if err := w.api.RespondPermission(pollCtx, p.SessionID, p.ID, w.directory, decision.Response); err != nil {
				util.Debug("agents.watchdog", "respond permission failed", map[string]any{"error": err})
				w.unmarkAnswered(p.ID)
				continue
			}
		}
	} else {
		util.Trace("agents.watchdog", "list permissions failed", map[string]any{"error": err})
	}

	if qs, err := w.api.ListQuestions(pollCtx, w.directory); err == nil {
		for _, q := range qs {
			if !w.ownsTransitive(pollCtx, q.SessionID) {
				continue
			}
			util.Warn("agents.watchdog", "auto-rejecting clarification question", map[string]any{
				"session_id": q.SessionID, "question_id": q.ID, "question": q.Question,
			})
			w.emit(events.Event{
				Kind:   events.KindWatchdogAction,
				JobID:  "watchdog.question",
				Status: "reject",
				Payload: map[string]any{
					"action":     "auto_reject_question",
					"session_id": q.SessionID,
					"question":   q.Question,
				},
			})
			if err := w.api.RejectQuestion(pollCtx, q.ID, w.directory); err != nil {
				util.Debug("agents.watchdog", "reject question failed", map[string]any{"error": err})
			}
		}
	} else {
		util.Trace("agents.watchdog", "list questions failed", map[string]any{"error": err})
	}
}
