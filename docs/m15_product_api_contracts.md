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

5. `GET /products/templates`
- Query: optional `path` for template catalog override
- Output: product query template catalog

6. `POST /products/templates/execute`
- Input: `template_id`, optional `vars`, optional `template_path`
- Output: resolved request metadata and product result payload

7. `GET /products/questions`
- Query: optional `catalog_path` override
- Output: M17 question catalog entries

8. `POST /products/questions/execute`
- Input: `question_id`, optional `vars`, optional `catalog_path`, optional `template_path`
- Output: resolved question endpoint execution result via mapped M15 template

9. `GET /products/questions/coverage`
- Query: optional `catalog_path`, optional `template_path`
- Output: per-question template mapping coverage with aggregate ratio

10. `POST /products/questions/run`
- Input: optional `question_ids`, optional `vars`, optional `vars_by_question`, optional `catalog_path`, optional `template_path`
- Output: batch execution summary (`total`, `succeeded`, `failed`, `overall_passed`) with per-question results

## Contract Rules
- Product endpoints must rely on graph query contracts only.
- Every endpoint supports explain mode for traceability.
- Tenant authorization and redaction policies apply before product computation.
