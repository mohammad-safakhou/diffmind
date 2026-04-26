package agents

// JSON schemas used with OpenCode PromptStructured calls. The server enforces
// these so the resulting payloads we parse back are already validated.

func entitySchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type":        map[string]any{"type": "string"},
			"name":        map[string]any{"type": "string"},
			"summary":     map[string]any{"type": "string"},
			"key_actions": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"confidence":  map[string]any{"type": "number"},
			"tags":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"details":     map[string]any{"type": "object", "additionalProperties": true},
			"inputs": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":        map[string]any{"type": "string"},
						"type":        map[string]any{"type": "string"},
						"required":    map[string]any{"type": "boolean"},
						"description": map[string]any{"type": "string"},
					},
				},
			},
			"source_locations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
					},
					"required": []string{"file", "start_line"},
				},
			},
			"evidence": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
						"snippet":    map[string]any{"type": "string"},
						"source":     map[string]any{"type": "string"},
					},
					"required": []string{"file", "start_line"},
				},
			},
		},
		"required": []string{"type", "name", "summary", "confidence", "source_locations"},
	}
}

func entityListSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "items": entitySchema()},
		},
		"required": []string{"items"},
	}
}

func entitySingleSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item": entitySchema(),
		},
	}
}

func conditionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":        map[string]any{"type": "string"},
			"expression":  map[string]any{"type": "string"},
			"variables":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"operator":    map[string]any{"type": "string"},
			"value":       map[string]any{"type": "string"},
			"negated":     map[string]any{"type": "boolean"},
			"explanation": map[string]any{"type": "string"},
		},
	}
}

func connectionStepSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"order":     map[string]any{"type": "number"},
			"action":    map[string]any{"type": "string"},
			"operation": map[string]any{"type": "string"},
			"from":      map[string]any{"type": "string"},
			"to":        map[string]any{"type": "string"},
			"condition": conditionSchema(),
			"location": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":       map[string]any{"type": "string"},
					"start_line": map[string]any{"type": "number"},
					"end_line":   map[string]any{"type": "number"},
				},
			},
			"evidence": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object", "additionalProperties": true},
			},
		},
	}
}

func connectionPathSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":        map[string]any{"type": "string"},
			"summary":   map[string]any{"type": "string"},
			"condition": conditionSchema(),
			"steps":     map[string]any{"type": "array", "items": connectionStepSchema()},
		},
	}
}

func connectionSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"from_exposure_id": map[string]any{"type": "string"},
			"to_dependency_id": map[string]any{"type": "string"},
			"summary":          map[string]any{"type": "string"},
			"confidence":       map[string]any{"type": "number"},
			"path_signature":   map[string]any{"type": "string"},
			"condition":        conditionSchema(),
			"paths":            map[string]any{"type": "array", "items": connectionPathSchema()},
			"source_locations": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file":       map[string]any{"type": "string"},
						"start_line": map[string]any{"type": "number"},
						"end_line":   map[string]any{"type": "number"},
					},
				},
			},
			"evidence": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object", "additionalProperties": true},
			},
		},
		"required": []string{"from_exposure_id", "to_dependency_id", "summary", "confidence"},
	}
}

func connectionListSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{"type": "array", "items": connectionSchema()},
		},
		"required": []string{"items"},
	}
}

func repoFactsSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"service_name":        map[string]any{"type": "string"},
			"languages":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"frameworks":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"build_files":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"config_files":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"monorepo_subdir":     map[string]any{"type": "string"},
			"probable_tech_hints": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"deployment_hints":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"extra_observations":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"module_map": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":    map[string]any{"type": "string"},
						"purpose": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}
