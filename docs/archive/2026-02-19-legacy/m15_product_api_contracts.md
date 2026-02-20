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

3. `GET /products/topology/{graph_id}`
- Query: optional `service`, optional `include_sensitive`
- Output: internal topology and external dependency summary (fan-in/fan-out/cycle and edge mix)
- Explain: `?explain=true`

4. `GET /products/company/{graph_id}`
- Query: optional `include_sensitive`
- Output: cross-repo canonical identity summary (canonical service/queue/db/host clusters and alias edges)
- Explain: `?explain=true`

5. `GET /products/mapper/{graph_id}`
- Query: required `service`, optional `include_sensitive`
- Output: impact subgraph and node/edge counts
- Explain: `?explain=true`

6. `GET /products/governance/{graph_id}`
- Query: optional `include_sensitive`
- Output: risk posture, conflict/sensitive/verification rollups
- Explain: `?explain=true`

7. `GET /products/trust/{graph_id}`
- Query: optional `include_sensitive`
- Output: confidence posture, conflict store summary, and adjudication rollups
- Explain: `?explain=true`

8. `GET /products/runtime/{graph_id}`
- Query: optional `service`, optional `include_sensitive`
- Output: runtime/build/deploy/CI intelligence summary with graph-derived counts
- Explain: `?explain=true`

9. `GET /products/architecture/{graph_id}`
- Query: optional `service`, optional `include_sensitive`
- Output: architecture task-suite report, focused node/subgraph, and trace summary
- Explain: `?explain=true`

10. `GET /products/templates`
- Query: optional `path` for template catalog override
- Output: product query template catalog

11. `POST /products/templates/execute`
- Input: `template_id`, optional `vars`, optional `template_path`, optional `dry_run`
- Output: resolved request metadata and product result payload
- `dry_run=true` resolves and validates request shape without executing product handler

12. `GET /products/templates/validate`
- Query: optional `path` template catalog override, optional `catalog_path` question catalog override
- Output: template contract validation, question coverage ratio, and orphan-template diagnostics
- Contract validation enforces path-specific method and product-family compatibility.
- Contract validation enforces question/template variable compatibility (question endpoint placeholders must be satisfiable by mapped template placeholders).

13. `GET /products/questions`
- Query: optional `catalog_path` override
- Output: M17 question catalog entries

14. `POST /products/questions/execute`
- Input: `question_id`, optional `vars`, optional `catalog_path`, optional `template_path`
- Output: resolved question endpoint execution result via mapped M15 template

15. `GET /products/questions/coverage`
- Query: optional `catalog_path`, optional `template_path`
- Output: per-question template mapping coverage with aggregate ratio

16. `POST /products/questions/run`
- Input: optional `question_ids`, optional `vars`, optional `vars_by_question`, optional `catalog_path`, optional `template_path`
- Output: batch execution summary (`total`, `succeeded`, `failed`, `overall_passed`) with per-question results

## Related Graph Endpoints
1. `GET /graphs/{graph_id}/architecture-tasks`
- Query: optional `path`, optional `focus_node_id`, optional `include_graph_data`
- Output: persisted or computed architecture task report (with optional focused subgraph)

2. `POST /graphs/{graph_id}/architecture-tasks`
- Input: optional `focus_node_id`, optional `out_path`, optional `export_subgraph`, optional `subgraph_out_path`, optional `include_graph_data`
- Output: persisted architecture task report path and optional focused subgraph artifact path

## Contract Rules
- Product endpoints must rely on graph query contracts only.
- Every endpoint supports explain mode for traceability.
- Tenant authorization and redaction policies apply before product computation.
- Template execution fails fast with deterministic `400` when required template variables are missing.
