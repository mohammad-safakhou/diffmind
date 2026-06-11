// Package llmrun owns LLM session execution and its transport-facing
// capabilities. Pipeline stages depend on these narrow contracts, not on the
// concrete OpenCode HTTP client.
package llmrun

import "context"

type Client interface {
	Enabled() bool
	CreateSession(ctx context.Context, directory string) (string, error)
	DeleteSession(ctx context.Context, sessionID, directory string) error
	PromptStructured(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (map[string]any, error)
	PromptText(ctx context.Context, sessionID, directory, prompt string) (string, error)
}

type PauseHandler interface {
	AbortSession(ctx context.Context, sessionID, directory string) error
	ListPermissions(ctx context.Context, directory string) ([]PendingPermission, error)
	RespondPermission(ctx context.Context, sessionID, permissionID, directory, response string) error
	ListQuestions(ctx context.Context, directory string) ([]PendingQuestion, error)
	RejectQuestion(ctx context.Context, requestID, directory string) error
	LookupSessionParent(ctx context.Context, sessionID, directory string) (string, error)
}

type VerbosePrompter interface {
	PromptStructuredVerboseRaw(ctx context.Context, sessionID, directory, prompt string, schema map[string]any) (parsed map[string]any, raw []byte, text string, err error)
}

type TokenReader interface {
	GetSession(ctx context.Context, sessionID, directory string) (SessionState, error)
}

type SessionState struct {
	ID         string
	Cost       float64
	Input      int
	Output     int
	Reasoning  int
	CacheRead  int
	CacheWrite int
}

type PendingPermission struct {
	ID         string
	SessionID  string
	Title      string
	Type       string
	Permission string
	Patterns   []string
}

type PendingQuestion struct {
	ID        string
	SessionID string
	Question  string
}
