package llmrun

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/opencode"
)

// pauseBridge adapts a *opencode.Client into the agents.pauseHandler
// interface. Keeping the bridge in this package means the agents tests can
// continue to swap in fakes without dragging the opencode package into them.
type PauseBridge struct {
	c *opencode.Client
}

func NewPauseBridge(c *opencode.Client) *PauseBridge {
	if c == nil {
		return nil
	}
	return &PauseBridge{c: c}
}

func (p *PauseBridge) AbortSession(ctx context.Context, sessionID, directory string) error {
	if p == nil || p.c == nil {
		return nil
	}
	return p.c.AbortSession(ctx, sessionID, directory)
}

func (p *PauseBridge) ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error) {
	if p == nil || p.c == nil {
		return nil, nil
	}
	in, err := p.c.ListPermissions(ctx, directory)
	if err != nil {
		return nil, err
	}
	out := make([]PendingPermission, 0, len(in))
	for _, v := range in {
		out = append(out, PendingPermission{
			ID:         v.ID,
			SessionID:  v.SessionID,
			Title:      v.Title,
			Type:       v.Type,
			Permission: v.Permission,
			Patterns:   append([]string(nil), v.Patterns...),
		})
	}
	return out, nil
}

func (p *PauseBridge) RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error {
	if p == nil || p.c == nil {
		return nil
	}
	return p.c.RespondPermission(ctx, sessionID, permissionID, directory, response)
}

func (p *PauseBridge) ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error) {
	if p == nil || p.c == nil {
		return nil, nil
	}
	in, err := p.c.ListQuestions(ctx, directory)
	if err != nil {
		return nil, err
	}
	out := make([]PendingQuestion, 0, len(in))
	for _, v := range in {
		out = append(out, PendingQuestion{
			ID: v.ID, SessionID: v.SessionID, Question: v.Question,
		})
	}
	return out, nil
}

func (p *PauseBridge) RejectQuestion(ctx context.Context, requestID, directory string) error {
	if p == nil || p.c == nil {
		return nil
	}
	return p.c.RejectQuestion(ctx, requestID, directory)
}

// LookupSessionParent returns the parentID field of an OpenCode session,
// or "" when the session is top-level / not found. We hit the cheap
// GET /session/{id} endpoint (~500B) and read SessionState.ParentID.
// The watchdog calls this only for permissions whose SessionID is not
// directly in ownedSessions, so it costs at most one round-trip per
// unrecognised session (cached afterwards).
func (p *PauseBridge) LookupSessionParent(ctx context.Context, sessionID, directory string) (string, error) {
	if p == nil || p.c == nil || sessionID == "" {
		return "", nil
	}
	s, err := p.c.GetSession(ctx, sessionID, directory)
	if err != nil {
		return "", err
	}
	return s.ParentID, nil
}
