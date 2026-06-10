package core

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/opencode"
)

// tokenBridge adapts an *opencode.Client into the agents package's
// narrow tokenReader interface. We keep the agents package free of
// the full opencode types so unit tests don't have to construct
// HTTP-shaped session records — they can build sessionState
// directly.
//
// The bridge is wired in RunWith next to the existing pause-handler
// and verbose-prompter bridges; when the underlying client does not
// implement opencode.SessionState (e.g. a slim test fake), we leave
// orchestrator.tokens nil and per-call token reads become no-ops.
type TokenBridge struct {
	c *opencode.Client
}

// NewTokenBridge wraps an *opencode.Client so the orchestrator (a
// different package) can build the bridge without reaching into core's
// unexported field.
func NewTokenBridge(c *opencode.Client) *TokenBridge {
	return &TokenBridge{c: c}
}

func (b *TokenBridge) GetSession(ctx context.Context, sessionID, directory string) (SessionState, error) {
	s, err := b.c.GetSession(ctx, sessionID, directory)
	if err != nil {
		return SessionState{}, err
	}
	return SessionState{
		ID:         s.ID,
		Cost:       s.Cost,
		Input:      s.Tokens.Input,
		Output:     s.Tokens.Output,
		Reasoning:  s.Tokens.Reasoning,
		CacheRead:  s.Tokens.Cache.Read,
		CacheWrite: s.Tokens.Cache.Write,
	}, nil
}
