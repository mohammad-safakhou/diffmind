package agents

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
type watchdog struct {
	api           pauseHandler
	directory     string
	pollInterval  time.Duration
	sink          events.Sink
	mu            sync.Mutex
	ownedSessions map[string]struct{}
	answered      map[string]struct{} // permissions we have already responded to
	stopCh        chan struct{}
	doneCh        chan struct{}
	stopOnce      sync.Once
}

func (w *watchdog) markAnswered(permID string) {
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

func newWatchdog(api pauseHandler, directory string, poll time.Duration) *watchdog {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	return &watchdog{
		api:           api,
		directory:     directory,
		pollInterval:  poll,
		ownedSessions: map[string]struct{}{},
		answered:      map[string]struct{}{},
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
}

// SetSink wires a live event sink so watchdog actions are observable from
// the dashboard.
func (w *watchdog) SetSink(s events.Sink) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.sink = s
	w.mu.Unlock()
}

func (w *watchdog) emit(e events.Event) {
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
func (w *watchdog) Track(sessionID string) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	w.ownedSessions[sessionID] = struct{}{}
	w.mu.Unlock()
}

// Untrack removes a session id (e.g. after DeleteSession). It is safe to
// call multiple times.
func (w *watchdog) Untrack(sessionID string) {
	if w == nil || sessionID == "" {
		return
	}
	w.mu.Lock()
	delete(w.ownedSessions, sessionID)
	w.mu.Unlock()
}

func (w *watchdog) owns(sessionID string) bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.ownedSessions[sessionID]
	return ok
}

// Start launches the polling goroutine. It is a no-op if api is nil.
func (w *watchdog) Start(ctx context.Context) {
	if w == nil || w.api == nil {
		if w != nil {
			close(w.doneCh)
		}
		return
	}
	go w.loop(ctx)
}

// Stop signals the watchdog to exit and waits for the goroutine to finish.
func (w *watchdog) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() { close(w.stopCh) })
	<-w.doneCh
}

func (w *watchdog) loop(ctx context.Context) {
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

func (w *watchdog) tick(ctx context.Context) {
	// Use a short, independent timeout for each poll so a slow server can't
	// stall the watchdog itself.
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	w.mu.Lock()
	answered := w.answered
	w.mu.Unlock()

	if perms, err := w.api.ListPermissions(pollCtx, w.directory); err == nil {
		for _, p := range perms {
			if !w.owns(p.SessionID) {
				continue
			}
			// Skip permissions we already answered. OpenCode keeps them in
			// the GET /permission listing for a few seconds after we
			// reply, so we'd otherwise emit the same auto-deny / auto-allow
			// every poll — confusing and noisy.
			if _, seen := answered[p.ID]; seen {
				continue
			}
			decision := decidePermission(p, w.directory)
			if decision.Response == "" {
				continue
			}
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
				continue
			}
			w.markAnswered(p.ID)
		}
	} else {
		util.Trace("agents.watchdog", "list permissions failed", map[string]any{"error": err})
	}

	if qs, err := w.api.ListQuestions(pollCtx, w.directory); err == nil {
		for _, q := range qs {
			if !w.owns(q.SessionID) {
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
