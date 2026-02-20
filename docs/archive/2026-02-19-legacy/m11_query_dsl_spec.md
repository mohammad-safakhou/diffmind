# M11 Query DSL Specification

This document defines the query request schema for `POST /query/execute`.

## Request

```json
{
  "graph_id": "string (required)",
  "type": "string (optional)",
  "edge_type": "string (optional, comma-separated edge types)",
  "service_id": "string (optional)",
  "repo_path": "string (optional)",
  "node_id": "string (optional)",
  "section": "string (optional)",
  "class": "string (optional)",
  "verification_state": "string (optional)",
  "adapter_id": "string (optional)",
  "provenance_version": "string (optional)",
  "conflict_status": "string (optional)",
  "environment": "string (optional)",
  "q": "string (optional free-text)",
  "confidence_min": "number [0..1] (optional)",
  "include_inferred": "bool (optional, default false)",
  "include_disputed": "bool (optional, default false)",
  "include_sensitive": "bool (optional, default false)",
  "explain": "bool (optional, default false)",
  "node_limit": "int >= 0 (optional)",
  "edge_limit": "int >= 0 (optional)",
  "max_age_hours": "int > 0 (optional, default 24)"
}
```

## Semantics

1. Filters are applied over graph nodes and edges.
2. Strict publish policy is applied after filtering.
3. Tenant-aware redaction is applied before output.
4. Pagination caps are applied via `node_limit` and `edge_limit`.
5. `explain=true` returns graph explain payload with provenance/evidence/confidence details.

## Saved Query Template API

1. `GET /query/templates`
2. `GET /query/templates/validate`
3. `POST /query/templates/execute`

Default template catalog path:

`docs/m11_query_templates.json`

## Saved Template Contracts

1. Query template method must be `POST`.
2. Query template path must be `/query/execute`.
3. Template payload must resolve to a valid query request with non-empty `graph_id`.
4. Template execution fails fast with deterministic `400` if required placeholder vars are missing (`missing_vars` list included).
