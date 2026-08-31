package protocol

func JSONSchema() map[string]any {
	return map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"$id":      "https://diffmind.local/schemas/diffmind.service.v1.json",
		"title":    "Diffmind Service Context Protocol v1",
		"type":     "object",
		"required": []string{"schema", "service", "objects"},
		"properties": map[string]any{
			"schema": map[string]any{"const": SchemaServiceV1},
			"service": map[string]any{
				"type":     "object",
				"required": []string{"id", "name"},
				"properties": map[string]any{
					"id":          map[string]any{"type": "string"},
					"name":        map[string]any{"type": "string"},
					"team":        map[string]any{"type": "string"},
					"domain":      map[string]any{"type": "string"},
					"criticality": map[string]any{"type": "string"},
				},
			},
			"repository":   map[string]any{"type": "object", "additionalProperties": true},
			"objects":      map[string]any{"type": "object", "additionalProperties": true},
			"flows":        map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true}},
			"observations": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true}},
			"evidence":     map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": true}},
			"metadata":     map[string]any{"type": "object", "additionalProperties": true},
		},
	}
}
