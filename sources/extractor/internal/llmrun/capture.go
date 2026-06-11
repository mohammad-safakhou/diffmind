package llmrun

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/extraction"
)

// CaptureStore persists the exact prompt and provider response bytes for a
// call. Capture failures are intentionally ignored because diagnostics must
// never change extraction behavior.
type CaptureStore struct {
	Dir string
}

func (s CaptureStore) Prompt(role, prompt string) {
	if s.Dir == "" || role == "" {
		return
	}
	_ = os.WriteFile(s.Path(role, "prompt", "txt"), []byte(prompt), 0o644)
}

func (s CaptureStore) Response(role string, payload map[string]any, raw []byte, text string) {
	if s.Dir == "" || role == "" {
		return
	}
	base := filepath.Join(s.Dir, extraction.SafeJobID(role))
	if payload != nil {
		if body, err := json.MarshalIndent(payload, "", "  "); err == nil {
			_ = os.WriteFile(base+".response.json", body, 0o644)
		}
	}
	if len(raw) > 0 {
		_ = os.WriteFile(base+".response.raw", raw, 0o644)
	}
	if strings.TrimSpace(text) != "" {
		_ = os.WriteFile(base+".response.text", []byte(text), 0o644)
	}
}

func (s CaptureStore) Path(jobID, kind, extension string) string {
	if s.Dir == "" || jobID == "" {
		return ""
	}
	return filepath.Join(s.Dir, extraction.SafeJobID(jobID)+"."+kind+"."+extension)
}
