# M15 Product API Contracts

All product endpoints are powered by queryable graph truth and never read raw source files directly.

## Endpoints

1. `POST /products/pr-review`
- Input: `graph_id`, optional `changed_nodes`, optional `max_findings`
- Output: prioritized findings and severity summary
- Explain: `?explain=true`

2. `GET /products/docs/{graph_id}`
- Query: optional `service`, optional `include_sensitive`
- Output: architecture/operations documentation payload
- Explain: `?explain=true`

3. `GET /products/mapper/{graph_id}`
- Query: required `service`, optional `include_sensitive`
- Output: impact subgraph and node/edge counts
- Explain: `?explain=true`

4. `GET /products/governance/{graph_id}`
- Query: optional `include_sensitive`
- Output: risk posture, conflict/sensitive/verification rollups
- Explain: `?explain=true`

## Contract Rules
- Product endpoints must rely on graph query contracts only.
- Every endpoint supports explain mode for traceability.
- Tenant authorization and redaction policies apply before product computation.
