package llmrun

import (
	"context"

	"github.com/mohammad-safakhou/diffmind/internal/opencode"
)

// verboseBridge adapts the real *opencode.Client into the verbosePrompter
// interface so the orchestrator can recover the raw response body and any
// free-text parts when the structured slot couldn't be parsed. Keeping the
// adapter local to this package means tests fakes that don't need this
// extra surface stay focused on the smaller openCodeAPI interface.
type VerboseBridge struct {
	c *opencode.Client
}

func NewVerboseBridge(c *opencode.Client) *VerboseBridge {
	if c == nil {
		return nil
	}
	return &VerboseBridge{c: c}
}

func (v *VerboseBridge) PromptStructuredVerboseRaw(
	ctx context.Context,
	sessionID, directory, prompt string,
	schema map[string]any,
) (map[string]any, []byte, string, error) {
	if v == nil || v.c == nil {
		return nil, nil, "", nil
	}
	res, err := v.c.PromptStructuredVerbose(ctx, sessionID, directory, prompt, schema)
	return res.Payload, res.RawBody, res.TextBody, err
}
