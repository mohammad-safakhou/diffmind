package llmrun

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mohammad-safakhou/diffmind/internal/events"
	"github.com/mohammad-safakhou/diffmind/internal/util"
)

type SessionTracker interface {
	Track(sessionID string)
	Untrack(sessionID string)
}

type SessionOptions struct {
	Client      Client
	Pauser      PauseHandler
	Tracker     SessionTracker
	Sink        events.Sink
	Directory   string
	Reuse       bool
	Cleanup     bool
	DeleteDelay time.Duration
}

// SessionManager owns OpenCode session identity and cleanup. Prompt execution
// asks for a session and invokes the returned cleanup function after it has
// collected response diagnostics and token usage.
type SessionManager struct {
	client      Client
	pauser      PauseHandler
	tracker     SessionTracker
	sink        events.Sink
	directory   string
	reuse       bool
	cleanup     bool
	deleteDelay time.Duration

	mu       sync.Mutex
	sharedID string
}

func NewSessionManager(options SessionOptions) *SessionManager {
	delay := options.DeleteDelay
	if delay <= 0 {
		delay = 5 * time.Second
	}
	sink := options.Sink
	if sink == nil {
		sink = events.NoopSink{}
	}
	return &SessionManager{
		client:      options.Client,
		pauser:      options.Pauser,
		tracker:     options.Tracker,
		sink:        sink,
		directory:   options.Directory,
		reuse:       options.Reuse,
		cleanup:     options.Cleanup,
		deleteDelay: delay,
	}
}

func (m *SessionManager) Acquire(ctx context.Context, role string) (string, func(), error) {
	if m == nil || m.client == nil {
		return "", nil, fmt.Errorf("%s create session: client is not configured", role)
	}
	if m.reuse {
		m.mu.Lock()
		defer m.mu.Unlock()
		if strings.TrimSpace(m.sharedID) != "" {
			return m.sharedID, nil, nil
		}
		sessionID, err := m.client.CreateSession(ctx, m.directory)
		if err != nil {
			return "", nil, fmt.Errorf("%s create shared session: %w", role, err)
		}
		m.sharedID = sessionID
		m.track(sessionID)
		m.sink.Emit(events.Event{
			Kind: events.KindSessionCreated, JobID: role,
			Payload: map[string]any{"session_id": sessionID, "shared": true, "directory": m.directory},
		})
		util.Debug("agents.agent", "shared session created", map[string]any{"role": role, "session_id": sessionID})
		return sessionID, nil, nil
	}

	sessionID, err := m.client.CreateSession(ctx, m.directory)
	if err != nil {
		return "", nil, fmt.Errorf("%s create session: %w", role, err)
	}
	m.track(sessionID)
	m.sink.Emit(events.Event{
		Kind: events.KindSessionCreated, JobID: role,
		Payload: map[string]any{"session_id": sessionID, "shared": false, "directory": m.directory},
	})
	return sessionID, func() { m.scheduleDelete(role, sessionID) }, nil
}

func (m *SessionManager) Abort(role, sessionID string) {
	if m == nil || m.pauser == nil || sessionID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.pauser.AbortSession(ctx, sessionID, m.directory); err != nil {
		util.Debug("agents.agent", "abort session failed", map[string]any{
			"role": role, "session_id": sessionID, "error": err,
		})
		return
	}
	m.sink.Emit(events.Event{
		Kind: events.KindSessionAborted, JobID: role,
		Payload: map[string]any{"session_id": sessionID},
	})
	util.Trace("agents.agent", "session aborted", map[string]any{"role": role, "session_id": sessionID})
}

func (m *SessionManager) ResetAfterStuck() {
	if m == nil || !m.reuse {
		return
	}
	m.mu.Lock()
	sessionID := strings.TrimSpace(m.sharedID)
	m.sharedID = ""
	m.mu.Unlock()
	m.untrack(sessionID)
}

func (m *SessionManager) Close() {
	if m == nil || !m.reuse || !m.cleanup {
		return
	}
	m.mu.Lock()
	sessionID := strings.TrimSpace(m.sharedID)
	m.sharedID = ""
	m.mu.Unlock()
	if sessionID == "" {
		return
	}
	if err := m.client.DeleteSession(context.Background(), sessionID, m.directory); err != nil {
		util.Warn("agents.agent", "shared session delete failed", map[string]any{
			"session_id": sessionID, "error": err,
		})
	}
	m.untrack(sessionID)
}

func (m *SessionManager) scheduleDelete(role, sessionID string) {
	if !m.cleanup || strings.TrimSpace(sessionID) == "" {
		return
	}
	go func() {
		time.Sleep(m.deleteDelay)
		if err := m.client.DeleteSession(context.Background(), sessionID, m.directory); err != nil {
			util.Warn("agents.agent", "session delete failed", map[string]any{
				"role": role, "session_id": sessionID, "error": err,
			})
			return
		}
		m.untrack(sessionID)
		util.Trace("agents.agent", "session deleted", map[string]any{"role": role, "session_id": sessionID})
	}()
}

func (m *SessionManager) track(sessionID string) {
	if m.tracker != nil {
		m.tracker.Track(sessionID)
	}
}

func (m *SessionManager) untrack(sessionID string) {
	if m.tracker != nil && sessionID != "" {
		m.tracker.Untrack(sessionID)
	}
}
