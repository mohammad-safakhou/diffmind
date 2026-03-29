package extractors

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mohammad-safakhou/diffmind/internal/opencode"
)

// ExtractWithLLM sends file contents and a prompt hint to the LLM for
// complex, multi-step extraction that cannot be done deterministically.
func ExtractWithLLM(client *opencode.Client, sessionID string, filePaths []string, promptHint string, extractFields []string) (map[string]any, error) {
	// Build context with file contents.
	var sb strings.Builder
	sb.WriteString("You are analyzing infrastructure configuration files to extract service identity information.\n\n")

	for _, fp := range filePaths {
		data, err := os.ReadFile(fp)
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("=== File: %s ===\n", fp))
		sb.WriteString(string(data))
		sb.WriteString("\n\n")
	}

	sb.WriteString("TASK:\n")
	sb.WriteString(promptHint)
	sb.WriteString("\n\nReturn a JSON object with the following keys: ")
	sb.WriteString(strings.Join(extractFields, ", "))
	sb.WriteString("\n\nReturn ONLY valid JSON, no explanation.")

	// Define a simple schema.
	props := make(map[string]any)
	for _, f := range extractFields {
		props[f] = map[string]any{"type": "string"}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	schemaBytes, _ := json.Marshal(schema)

	raw, err := client.PromptStructured(sessionID, sb.String(), schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("LLM extraction failed: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse LLM extraction result: %w", err)
	}
	return result, nil
}
