package extraction

import (
	"fmt"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/model"
)

// repair_prompt.go builds the Stage-4.5 connection-repair prompt (A1). The
// deterministic walk cannot cross DI/interface, async-dispatch, or some cron
// boundaries, so exposures it left with zero connections get ONE evidence-
// gated LLM pass. The model picks targets from a CLOSED set of existing
// dependency IDs — it may never invent a dependency — and must cite the call
// chain it found; the stage validates that evidence before accepting anything.

// RepairCandidate is one row of the closed dependency set offered to the model.
type RepairCandidate struct {
	ID       string
	Type     string
	Name     string
	Instance string
	Location string // "file:line" of the dependency's first source location
}

func BuildConnectionRepairPrompt(exposures []model.Exposure, candidates []RepairCandidate, rf *RepoFacts, subDir string) string {
	var sb strings.Builder
	sb.WriteString("AGENT ROLE: connection-repair\n")
	sb.WriteString(readOnlyPreamble)
	sb.WriteString(MonorepoScopeLine(subDir))
	sb.WriteString(RepoFactsBlock(rf))

	sb.WriteString("UNCONNECTED_EXPOSURES (walk found no dependency reachable from these):\n")
	for _, e := range exposures {
		loc := ""
		if len(e.Locations) > 0 {
			loc = fmt.Sprintf("  [%s:%d]", e.Locations[0].File, e.Locations[0].StartLine)
		}
		handler := ""
		if h, ok := e.Details["handler"].(string); ok && h != "" {
			handler = "  handler=" + h
		}
		fmt.Fprintf(&sb, "- id=%s  type=%s  name=%q%s%s\n", e.ID, e.Type, e.Name, handler, loc)
	}

	sb.WriteString("\nKNOWN_DEPENDENCIES (the ONLY valid connection targets):\n")
	for _, c := range candidates {
		row := fmt.Sprintf("- id=%s  type=%s  name=%q", c.ID, c.Type, c.Name)
		if c.Instance != "" {
			row += "  instance=" + c.Instance
		}
		if c.Location != "" {
			row += "  [" + c.Location + "]"
		}
		sb.WriteString(row + "\n")
	}

	sb.WriteString(`
TASK:
The static call-graph walk could not connect the exposures above to any
dependency. Read the code and determine which of the KNOWN_DEPENDENCIES each
exposure actually reaches at runtime through paths a static walker misses:
dependency injection behind interfaces, event/async dispatch, scheduled
invocation, framework callbacks, or indirection through configuration.

HARD RULES:
- to_dependency_id and from_exposure_id MUST be ids from the lists above.
  NEVER invent, derive, or modify an id.
- Report a connection ONLY when you traced the concrete call chain in the
  code. Cite every file you used as evidence with exact line numbers; the
  citation is validated and a connection with bad evidence is discarded.
- An exposure that genuinely reaches nothing is a valid answer: omit it.
- confidence reflects how certain you are the runtime path exists, in [0,1].

OUTPUT: Return a single JSON object {"connections": [...]} matching the schema.`)
	return sb.String()
}

// ConnectionRepairSchema is the structured-output contract for the repair call.
func ConnectionRepairSchema() map[string]any {
	evidence := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file":       map[string]any{"type": "string"},
			"start_line": map[string]any{"type": "integer"},
			"end_line":   map[string]any{"type": "integer"},
			"snippet":    map[string]any{"type": "string"},
		},
		"required": []string{"file", "start_line"},
	}
	conn := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from_exposure_id": map[string]any{"type": "string"},
			"to_dependency_id": map[string]any{"type": "string"},
			"summary":          map[string]any{"type": "string"},
			"confidence":       map[string]any{"type": "number"},
			"evidence":         map[string]any{"type": "array", "items": evidence},
		},
		"required": []string{"from_exposure_id", "to_dependency_id", "evidence", "confidence"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"connections": map[string]any{"type": "array", "items": conn},
		},
		"required": []string{"connections"},
	}
}
