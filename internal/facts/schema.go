package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const JSONSchemaV1 = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://diffmind.dev/schemas/fact-bundle-v1.json",
  "title": "DiffMind Fact Bundle",
  "type": "object",
  "required": ["facts", "evidence"],
  "properties": {
    "facts": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "type", "evidence_ids", "confidence", "provenance"],
        "properties": {
          "id": {"type": "string"},
          "type": {"type": "string"},
          "attributes": {"type": "object"},
          "evidence_ids": {
            "type": "array",
            "items": {"type": "string"},
            "minItems": 1
          },
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "provenance": {
            "type": "object",
            "required": ["analyzer_id", "analyzer_version", "deterministic", "inferred"],
            "properties": {
              "analyzer_id": {"type": "string"},
              "analyzer_version": {"type": "string"},
              "deterministic": {"type": "boolean"},
              "inferred": {"type": "boolean"}
            }
          }
        }
      }
    },
    "evidence": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "snapshot_id", "file_path", "start_line", "start_col", "end_line", "end_col", "snippet_hash"],
        "properties": {
          "id": {"type": "string"},
          "snapshot_id": {"type": "string"},
          "file_path": {"type": "string"},
          "start_line": {"type": "integer", "minimum": 1},
          "start_col": {"type": "integer", "minimum": 1},
          "end_line": {"type": "integer", "minimum": 1},
          "end_col": {"type": "integer", "minimum": 1},
          "snippet_hash": {"type": "string"},
          "ast_node_id": {"type": "string"},
          "query_name": {"type": "string"}
        }
      }
    }
  }
}`

func StableID(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(strings.TrimSpace(part)))
		_, _ = h.Write([]byte("|"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
