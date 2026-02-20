# Milestone Execution Status (Verified)

Last updated: 2026-02-19

## Summary
- M4 closeout: **PASS** (full contract checks and evidence on `checkout-service`).
- M10 verifier upgrade (strict evidence + low-confidence queue): **PASS**.
- M11 query pagination/performance controls: **PASS**.
- M11 query DSL HTTP serving + saved query template API: **PASS**.
- M11 query template variable-contract hardening (validate + execute fail-fast): **PASS**.
- M12 temporal compare-by-time query flow: **PASS**.
- M11 query CLI filter/pagination contract: **PASS**.
- M12 compare impacted-surface endpoint: **PASS**.
- M12 compare history temporal filters: **PASS**.
- M12 compare impact neighborhood subgraph endpoint: **PASS**.
- M12 incremental recompute planning API (plan/history/subgraph): **PASS**.
- M12 compare/incremental contract hardening (hops bounds, time filters, explain payloads): **PASS**.
- M13 compliance tenant-scope hardening + export range validation: **PASS**.
- M13 encrypted audit export key/KMS gate enforcement: **PASS**.
- M13 tamper-evident audit hash chain + integrity API: **PASS**.
- M13 signed export manifests + export verification API: **PASS**.
- M13 security evidence bundle generation API: **PASS**.
- M13 security evidence bundle retrieval/list/checksum hardening: **PASS**.
- M14 quality HTTP API surface (evaluate/gate/report/dashboard/triage): **PASS**.
- M15 product template validation + dry-run contract hardening: **PASS**.
- M15 product template path-method-product contract hardening (architecture template included): **PASS**.
- M15 template variable-contract hardening (question/template placeholder compatibility + fail-fast execution): **PASS**.
- M16 operations API expansion (backup/restore/rollout/drill + policy): **PASS**.
- M16 SLO breach incident management API (evaluate/incidents/history): **PASS**.
- M17 final closeout API surface (closeout orchestration + artifact retrieval): **PASS**.
- M5 runtime/build/deploy product intelligence endpoint: **PASS**.
- M5 architecture graph UX hardening (service-anchor noise suppression + navigation precision): **PASS**.
- M6 release gate harness (multi-source E2E scorecard): **PASS**.
- M6 release gate architecture task-suite hardening (E2E task artifacts + strict gate): **PASS**.
- M7 dependency/internal topology product intelligence endpoint: **PASS**.
- M8 cross-repo/company canonical identity product intelligence endpoint: **PASS**.
- M9 confidence/conflict/adjudication API + trust product surface: **PASS**.
- M10 verification plane HTTP API (run/history/report/queue): **PASS**.
- Program closeout (M0-M17): **PASS** on validated E2E dataset.

## Verified Artifacts

### M4 Closure
- Report: `/Users/developer/repos/mine/diffmind/.diffmind/m4-closeout/m4_closure_report.json`
- Contract: `/Users/developer/repos/mine/diffmind/.diffmind/m4-closeout/m4_expected_contract.json`
- Runbook note: `/Users/developer/repos/mine/diffmind/docs/m4_closeout_status.md`

M4 checks passed:
1. non-builtin adapter execution (`gopls`)
2. replay parity
3. Java outbound false-positive suppression
4. queue target resolution via config bindings
5. default exclusion of test-scope architecture noise
6. Spring profile merge + code-reference linkage

### M0-M17 Closeout (E2E)
- Readiness report:
  - `/Users/developer/repos/mine/diffmind/.diffmind/e2e-m8-fixture-noaudit/final/readiness_report.json`
- Milestone closure report:
  - `/Users/developer/repos/mine/diffmind/.diffmind/e2e-m8-fixture-noaudit/final/milestone_closure_report.json`
- Security validation report:
  - `/Users/developer/repos/mine/diffmind/.diffmind/e2e-m8-fixture-noaudit/final/security_validation_report.json`
- Operations drill report:
  - `/Users/developer/repos/mine/diffmind/.diffmind/e2e-m8-fixture-noaudit/final/operations_drill_report.json`

Observed status:
- `readiness_report.overall_passed = true`
- `milestone_closure_report.overall_passed = true`
- No failed milestones in closure report.

### M10 Verifier Upgrade (Queue + Contract + Two-Pass)
- Verified pipeline run:
  - `/Users/developer/repos/mine/diffmind/.diffmind/w7-w10-verify/verify/report.json`
  - `/Users/developer/repos/mine/diffmind/.diffmind/w7-w10-verify/verify/review_queue.json`
- Verified strict-evidence scenario (critical claim without evidence):
  - report shows `missing_evidence_critical = 1`
  - review queue contains `p1` disputed item

M10 checks passed:
1. verifier emits persisted low-confidence/disputed queue artifact
2. strict evidence contract applied to critical claims
3. two-pass verification enabled (`hypothesis` then `contradiction`) with contradiction disputes tracked in verifier report
4. verifier output merged as decision entities and review-queue entity (no raw overwrite behavior)
5. full milestone sweep remains green after upgrade

### M10 Verification Plane HTTP API (Run + History + Retrieval)
- New verification endpoints:
  - `POST /verify/run`
  - `GET /verify/runs`
  - `GET /verify/runs/{run_id}`
  - `GET /verify/runs/{run_id}/report`
  - `GET /verify/runs/{run_id}/queue`
- Behavior:
  - verifier CLI workflow is now available as API-managed orchestration with persisted run index under graph root
  - strict evidence/two-pass/threshold options can be set per run request
  - deterministic report and review queue artifacts are retrievable by run id with tenant-scope enforcement

M10 API checks passed:
1. verification run endpoint executes verifier flow and persists run metadata/index.
2. run list endpoint provides tenant-scoped, paginated history filters.
3. run report/queue subresources return persisted verifier artifacts deterministically.
4. unauthorized requests are rejected and full milestone sweep remains green.

### M11 Query Pagination Controls
- Query endpoints now support:
  - `node_limit`
  - `edge_limit`
- Pagination metadata is attached to graph response:
  - `meta.query_pagination`

M11 checks passed:
1. `/graphs/{graph_id}/query` supports bounded graph result sets with truncation metadata.
2. `/graphs/at` supports bounded graph result sets with truncation metadata.
3. invalid pagination inputs return deterministic `400` errors.
4. regression and full milestone sweep remain green.

### M12 Temporal Compare Query
- Compare endpoint now supports temporal selection payload fields:
  - `from_at`
  - `to_at`
  - optional `mode`
- Existing `from_graph_id` / `to_graph_id` behavior remains unchanged.

M12 checks passed:
1. `POST /graphs/compare` can resolve graph snapshots by timestamp and compare them.
2. temporal compare input validation returns deterministic `400` for bad timestamps.
3. temporal compare returns deterministic `404` when no graph exists for requested time.
4. regression and full milestone sweep remain green.

### M11 Query CLI Filter Contract
- `extractor query` now supports:
  - `--type`
  - `--verification`
  - `--q`
  - `--confidence-min`
  - `--offset`
  - `--limit`
- JSON output now includes pagination metadata (`total`, `offset`, `limit`) and paged count.

M11 checks passed:
1. query CLI can filter by verification/confidence/text without API server dependency.
2. query CLI supports deterministic offset/limit pagination.
3. backward compatibility preserved for `FilterEntities(view)` used by HTTP API.
4. regression and full milestone sweep remain green.

### M11 Query DSL HTTP Serving + Saved Query Templates
- New query API endpoints:
  - `POST /query/execute`
  - `GET /query/templates`
  - `GET /query/templates/validate`
  - `POST /query/templates/execute`
- New M11 artifacts:
  - query DSL spec: `/Users/developer/repos/mine/diffmind/docs/m11_query_dsl_spec.md`
  - saved query templates: `/Users/developer/repos/mine/diffmind/docs/m11_query_templates.json`
- Endpoint behavior:
  - execute graph queries via JSON DSL with explain/pagination/redaction/publish-policy support.
  - execute saved query templates with variable interpolation and optional dry-run.
  - template validation enforces deterministic contract (`POST /query/execute` only).

M11 API checks passed:
1. query list/validate endpoints return deterministic template catalog contract.
2. `POST /query/execute` returns explain-capable filtered graph payload.
3. saved template execution routes through query DSL engine and returns deterministic result envelope.
4. unauthorized requests are rejected and full milestone sweep remains green.

### M11 Query Template Variable-Contract Hardening
- Query template validation now enforces payload-level request contract:
  - payload must resolve to a query request with non-empty `graph_id`.
- Query template execution now enforces required placeholder vars before execution:
  - missing template vars return deterministic `400` with `missing_vars`.
- Updated spec:
  - `/Users/developer/repos/mine/diffmind/docs/m11_query_dsl_spec.md`

M11 variable-contract checks passed:
1. `GET /query/templates/validate` rejects templates whose payload cannot resolve required query contract (`graph_id`).
2. `POST /query/templates/execute` rejects missing required vars deterministically (`missing template vars`).
3. targeted regressions and full E2E gates remain green (`go test ./internal/httpapi -count=1`, `./scripts/release_gate_m6.sh`, `./scripts/milestone_sweep.sh`).

### M12 Compare Impacted Surface
- New endpoint:
  - `GET /graphs/compare/{compare_id}/impact`
- Returns deterministic impacted sets:
  - `impacted_node_ids`
  - `impacted_edge_ids`
  - reason buckets (`added_nodes`, `removed_nodes`, `changed_nodes`, `added_edges`, `removed_edges`, `changed_edges`, `edge_endpoints`)

M12 checks passed:
1. compare artifacts can be queried for impacted surface directly.
2. endpoint is deterministic and stable for incremental/review tooling.
3. unknown compare subresources return deterministic `404`.
4. regression and full milestone sweep remain green.

### M12 Compare History Filters
- `GET /graphs/compare` now supports:
  - `from_graph_id`
  - `to_graph_id`
  - `from` (RFC3339 or unix seconds)
  - `to` (RFC3339 or unix seconds)

M12 checks passed:
1. compare history can be filtered by graph pair and time window.
2. invalid `from`/`to` timestamps return deterministic `400`.
3. `from > to` returns deterministic `400`.
4. regression and full milestone sweep remain green.

### M12 Compare Impact Neighborhood Subgraph
- New endpoint:
  - `GET /graphs/compare/{compare_id}/impact/subgraph`
- Query controls:
  - `hops` (default `1`, integer `>= 0`)
  - optional `graph_id` (defaults to compare `to_graph_id`)
- Returns compare-scoped neighborhood graph for impacted seeds:
  - `compare_id`
  - `graph_id`
  - `hops`
  - `seed_nodes`
  - `impact_graph`

M12 checks passed:
1. compare impact can be explored as a bounded neighborhood graph for UI drill-down.
2. invalid `hops` returns deterministic `400`.
3. invalid `graph_id` outside `{from_graph_id,to_graph_id}` returns deterministic `400`.
4. compare subresource routing supports `impact` and `impact/subgraph` deterministically.
5. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m12-impact-subgraph-hardening`).

### M12 Incremental Recompute Planning API
- New endpoints:
  - `POST /graphs/incremental`
  - `GET /graphs/incremental`
  - `GET /graphs/incremental/{plan_id}`
  - `GET /graphs/incremental/{plan_id}/subgraph`
- Endpoint behavior:
  - creates persisted incremental plans from `graph_id + changed_files + hops`.
  - computes seed nodes from node file attributes and edge evidence references.
  - returns impacted node/edge IDs plus neighborhood impact subgraph for targeted recompute workflows.
  - supports tenant-scoped history and cursor pagination for plan artifacts.

M12 incremental checks passed:
1. incremental planning endpoint creates deterministic plan artifacts with impacted sets and recommended action.
2. incremental history endpoint returns tenant-scoped plan summaries and supports filters.
3. incremental plan subgraph endpoint returns persisted impacted neighborhood graph.
4. unauthorized access is rejected and full milestone sweep remains green.

### M12 Compare/Incremental Contract Hardening
- Added bounded neighborhood hop contract for impact subgraph endpoints (`0..12`).
- Added explain-mode payloads for:
  - `GET /graphs/compare/{compare_id}/impact`
  - `GET /graphs/compare/{compare_id}/impact/subgraph`
  - `GET /graphs/incremental/{plan_id}/subgraph`
- Added strict incremental request validation:
  - normalized `changed_files` must remain non-empty
  - `hops` must satisfy bounded contract
- Added incremental history time-window filters (`from`, `to`) with deterministic validation.

M12 contract-hardening checks passed:
1. compare impact and impact-subgraph explain responses include deterministic selection metadata.
2. out-of-range hops and malformed time filters return deterministic `400`.
3. incremental planning rejects empty-normalized changed-files and invalid hops.
4. targeted regressions and full E2E gates remain green (`go test ./internal/httpapi -count=1`, `./scripts/release_gate_m6.sh`, `./scripts/milestone_sweep.sh`).

### M13 Compliance Tenant-Scope Hardening
- Compliance export endpoint (`POST /compliance/audit/export`) now enforces:
  - explicit `from <= to` validation for time windows
  - `tenant_id` override only for `platform_admin`
  - non-admin tenant override rejected with deterministic `403 tenant_mismatch`
- Compliance retention endpoint (`POST /compliance/audit/retention`) now enforces:
  - `tenant_id` override only for `platform_admin`
  - `all_tenants=true` allowed only for `platform_admin`
  - non-admin override/all-tenants requests rejected deterministically

M13 checks passed:
1. invalid export time windows return deterministic `400`.
2. tenant-scoped compliance operations reject cross-tenant override for non-admin actors.
3. platform admin can execute tenant-targeted export/retention.
4. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m13-compliance-hardening`).

### M13 Encrypted Audit Export Key/KMS Gates
- Compliance export endpoint (`POST /compliance/audit/export`) now enforces deterministic preflight checks for `encrypt=true`:
  - `DIFFMIND_AUDIT_EXPORT_KEY_B64` must exist
  - key must decode as base64 and be exactly 32 bytes
  - `DIFFMIND_KMS_KEY_ID` must exist
- Encrypted export returns explicit `encrypted=true`, `key_id`, and `.enc` output path.

M13 checks passed:
1. encrypted export without key material returns deterministic `400`.
2. encrypted export with valid key + KMS key id succeeds and emits encrypted artifact contract.
3. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m13-encryption-kms`).

### M13 Tamper-Evident Audit Integrity
- Audit events now include hash-chain fields:
  - `prev_hash`
  - `hash`
- Audit append computes deterministic event hash and chain linkage.
- New integrity verification API:
  - `GET /compliance/audit/integrity`
  - tenant-scoped checks for standard compliance users
  - full chain-enforced verification for platform-admin all-tenant mode

M13 checks passed:
1. tamper-evidence verification catches modified audit log events (`hash mismatch`).
2. integrity API enforces tenant/all-tenant access controls deterministically.
3. platform-admin can run all-tenant chain enforcement; tenant-scoped checks remain available.
4. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m13-audit-integrity`).

### M13 Signed Export Manifests And Verification
- Audit exports now emit signed manifest metadata:
  - payload digest (`payload_sha256`)
  - export scope metadata (tenant/time window/count)
  - optional HMAC signature fields when `DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64` is set
- New verification API:
  - `POST /compliance/audit/export/verify` with `manifest_path`
  - validates payload digest and signature (if present)
  - enforces tenant scope for non-platform callers

M13 checks passed:
1. exports produce manifest artifacts and signed metadata contract.
2. tampered export payload fails manifest verification deterministically.
3. verification API returns valid signed result for non-tampered exports.
4. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m13-export-manifest-signing`).

### M13 Security Evidence Bundle API
- New endpoint:
  - `POST /compliance/audit/evidence-bundle`
- Endpoint outputs a persisted artifact under:
  - `.diffmind/audit/evidence/security-evidence-bundle-*.json`
- Bundle includes:
  - audit integrity result
  - event summary and retention-delete preview
  - latest export manifest verification metadata
- Access controls:
  - non-platform users are tenant-scoped
  - `all_tenants=true` restricted to `platform_admin`

M13 checks passed:
1. endpoint emits persisted evidence-bundle artifact and response payload.
2. evidence bundle includes valid integrity and non-empty event summary for active tenant scope.
3. non-platform `all_tenants=true` is rejected deterministically with `403`.
4. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m13-security-evidence-bundle`).

### M13 Security Evidence Bundle Retrieval And Integrity
- Enhanced endpoint contract:
  - `POST /compliance/audit/evidence-bundle` now returns `checksum_sha256` with persisted artifact.
  - `GET /compliance/audit/evidence-bundle` lists bundles with checksum validity status.
  - `GET /compliance/audit/evidence-bundle?path=...` returns a specific bundle with `checksum_valid`.
- Access controls:
  - read/list is audit-read scoped and tenant-filtered
  - non-platform `all_tenants=true` denied with deterministic `403`

M13 checks passed:
1. created evidence bundles include checksum metadata and persisted artifact path.
2. list/read retrieval paths validate checksums deterministically.
3. tenant-scoped access control applies consistently for list/read and all-tenant mode.
4. regression and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m13-evidence-bundle-batch`).

### M14 Quality HTTP API Surface
- New endpoints:
  - `POST /quality/evaluate`
  - `POST /quality/gate`
  - `GET /quality/gate`
  - `GET /quality/report`
  - `GET /quality/dashboard`
  - `GET /quality/triage`
- Added request auth + audit behavior:
  - mutating quality actions use `build_graph` permissions and audit reasons
  - read quality artifact endpoints use graph-read permissions
- Added runbook usage:
  - `/Users/developer/repos/mine/diffmind/docs/m14_quality_runbook.md`

M14 checks passed:
1. HTTP evaluate flow emits report/dashboard/triage artifacts and returns payload for orchestration/UI.
2. HTTP gate flow returns deterministic pass/fail contract (`overall_passed`, `gate_error` when failed) and persists gate result artifact.
3. read endpoints return generated quality artifacts deterministically.
4. targeted regression tests and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m14-quality-api-batch`).

### M15 Product Contract Hardening (Template Validation + Dry-Run)
- New endpoint:
  - `GET /products/templates/validate`
- Enhanced endpoint contract:
  - `POST /products/templates/execute` accepts `dry_run=true` to resolve template request shape without executing product handlers.
- Validation contract includes:
  - duplicate/missing template id detection
  - unsupported HTTP method detection
  - non-executable template path detection
  - question-to-template mapping coverage
  - orphan-template diagnostics

M15 checks passed:
1. template validation endpoint returns deterministic `valid/error_count/warn_count/coverage_ratio` diagnostics.
2. template execute dry-run returns resolved metadata with `dry_run=true` and avoids downstream execution.
3. unauthenticated access remains blocked consistently for new product validation endpoint.
4. targeted regression tests and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m15-product-batch`).

### M15 Product Template Path/Method/Product Contract Hardening
- Expanded contract enforcement for product templates:
  - path-to-method gating (`/products/architecture/*` must be `GET`, etc.)
  - path-to-product-family gating (`/products/architecture/*` requires `product=architecture`, etc.)
  - handler-mapped path gating for executable product templates
- Updated product contracts documentation with architecture product endpoint and architecture-task graph endpoints:
  - `/Users/developer/repos/mine/diffmind/docs/m15_product_api_contracts.md`

M15 contract-hardening checks passed:
1. `GET /products/templates` includes `architecture-task-traces` in catalog output.
2. `GET /products/templates/validate` reports deterministic errors for method/path and product/path mismatches.
3. `POST /products/templates/execute` rejects contract-violating templates deterministically with `400`.
4. targeted regressions and E2E gates remain green (`go test ./internal/httpapi -count=1`, `./scripts/release_gate_m6.sh`, `./scripts/milestone_sweep.sh`).

### M15 Template Variable-Contract Hardening (Questions + Execution)
- Added fail-fast template variable validation before product template execution.
- Added question/template placeholder compatibility validation:
  - `GET /products/templates/validate` now rejects mappings where question endpoint placeholders are not satisfiable by mapped template placeholders.
  - `GET /products/questions/coverage` now reports per-item `contract_valid` and `contract_error`, plus aggregate `contract_valid_covered`.
- Added deterministic missing-variable behavior:
  - `POST /products/templates/execute` returns `400` with `missing_vars` when required vars are absent.
  - question execution path inherits same behavior through template execution.

M15 variable-contract checks passed:
1. invalid template/question catalogs surface deterministic placeholder mismatch errors.
2. question execution with missing required vars returns deterministic `400 missing template vars` including missing key names.
3. coverage endpoint returns contract-validity metadata for mapped question/template pairs.
4. targeted regressions and E2E gates remain green (`go test ./internal/httpapi -count=1`, `./scripts/release_gate_m6.sh`, `./scripts/milestone_sweep.sh`).

### M16 Operations API Expansion
- New operations endpoints:
  - `GET /ops/rollout-policy`
  - `POST /ops/backup`
  - `POST /ops/restore`
  - `POST /ops/rollout`
  - `POST /ops/drill`
- Security contract updates:
  - new action `operate_ops` (`tenant_admin` or `ops:write` / `audit:export`, plus platform-admin override)
- Drill behavior:
  - deterministic run of backup -> restore -> rollout -> slo with step-level error fields

M16 checks passed:
1. rollout policy, backup, restore, rollout, and full drill endpoints pass end-to-end with deterministic output artifacts.
2. unauthenticated access remains blocked on new ops endpoints.
3. security policy enforces dedicated ops-mutation authorization path.
4. targeted regression tests and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m16-ops-api-batch`).

### M16 SLO Breach Incident Management API
- New incident/alert endpoints:
  - `POST /ops/slo/evaluate`
  - `GET /ops/incidents`
  - `GET /ops/incidents/{incident_id}`
- Behavior:
  - computes current SLO payload and can persist incident records on breach (or forced drill).
  - incident index/history is persisted under ops artifacts and tenant-scoped for non-platform callers.
  - incident records include captured SLO payload for deterministic post-incident analysis.

M16 incident checks passed:
1. forced evaluation creates deterministic incident artifact with id and persisted payload.
2. incident list endpoint returns tenant-scoped incident history with status filtering/cursor support.
3. incident by-id endpoint returns persisted incident record and enforces tenant boundaries.
4. unauthorized access remains blocked and full milestone sweep remains green.

### M17 Final Closeout API Surface
- New closeout orchestration endpoint:
  - `POST /final/closeout`
- New final artifact read endpoints:
  - `GET /final/milestones`
  - `GET /final/benchmark`
  - `GET /final/security`
  - `GET /final/ops`
- Existing `POST /final/attest`, `GET /final/readiness`, and `GET /final/decision` remain unchanged and compatible.

M17 checks passed:
1. final closeout can be executed via HTTP with full parameterized output paths and returns generated report payloads.
2. milestone/benchmark/security/ops final reports are retrievable via dedicated GET endpoints.
3. unauthenticated access remains blocked on new final endpoints.
4. targeted regression tests and full milestone sweep remain green (`./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m17-closeout-api-batch`).

### M5 Runtime/Build/Deploy Product Intelligence
- New product endpoint:
  - `GET /products/runtime/{graph_id}`
- Product template + catalog coverage:
  - runtime template added to `/Users/developer/repos/mine/diffmind/docs/m15_query_templates.json`
  - runtime question added to `/Users/developer/repos/mine/diffmind/docs/m17_question_catalog.json`
- Endpoint behavior:
  - service-scoped runtime intelligence summary over graph entities and edges
  - supports `service`, `include_sensitive`, and `explain` query options
  - supports direct HTTP usage and template execution routing

M5 checks passed:
1. runtime product endpoint is routable and authorized under product query action.
2. template execution can route to runtime endpoint contract.
3. unauthenticated access is blocked; authenticated runtime request returns deterministic report payload.
4. targeted regression tests and full milestone sweep remain green (`GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1` and `./scripts/milestone_sweep.sh` with summary `/Users/developer/repos/mine/diffmind/.diffmind/milestone-sweep/summary.json`).

### M5 Graph UX Hardening (Architecture Readability)
- Updated UI view controls:
  - `showServiceAnchors` toggle added to suppress service-hub anchor edges in default topology view.
  - architecture filter now prioritizes architecture-bearing node types and excludes context-heavy defaults.
- Interaction hardening:
  - zoom buttons now anchor around current mouse position in the graph viewport.
  - wheel zoom sensitivity reduced for controlled navigation on dense graphs.
  - drag pan speed reduced to avoid jumpy navigation.
  - node hover now exposes full label/id/type via native tooltip; selection panel shows full node name explicitly.
- Behavior:
  - default graph view becomes less noisy on single-service graphs by hiding service-anchor spokes.
  - cross-service topology edges remain available when service-anchor overlay is enabled intentionally.

M5 UX checks passed:
1. UI graph controls compile/serve and apply service-anchor suppression without breaking graph rendering.
2. full node identity is available on hover/select even when on-canvas labels are truncated.
3. zoom/pan interactions are more precise and mouse-anchored in dense views.
4. targeted regression tests and full milestone sweep remain green (`GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1` and `./scripts/milestone_sweep.sh` with summary `/Users/developer/repos/mine/diffmind/.diffmind/milestone-sweep/summary.json`).

### M6 Release Gate Harness (Multi-Source E2E Scorecard)
- New executable harness:
  - `/Users/developer/repos/mine/diffmind/scripts/release_gate_m6.sh`
- New runbook:
  - `/Users/developer/repos/mine/diffmind/docs/m6_release_gate_runbook.md`
- Harness contract:
  - executes API contract regression tests
  - executes full E2E extraction/graph/quality/finalgate flow across multiple sources
  - emits per-source scorecards plus aggregate summary for:
    - accuracy
    - completeness
    - explainability
    - task pass-rate

M6 checks passed:
1. strict release gate summary passes for three fixture sources with `overall_passed=true`.
2. per-source scorecards are emitted with deterministic artifact references and metrics.
3. API contract checks run as first gate in harness and pass.
4. full milestone sweep remains green (`./scripts/milestone_sweep.sh`) after M6 harness addition.

### M6 Release Gate Architecture Task-Suite Hardening
- Harness upgrade:
  - `/Users/developer/repos/mine/diffmind/scripts/release_gate_m6.sh`
- Runbook update:
  - `/Users/developer/repos/mine/diffmind/docs/m6_release_gate_runbook.md`
- New per-source E2E artifacts:
  - `runs/<source>/tasks/architecture_tasks.json`
  - `runs/<source>/tasks/focused_subgraph.json`
- New gate behavior:
  - computes executable architecture tasks from built graph artifacts:
    - find exposures
    - trace endpoint to dependencies
    - identify queue consumers/publishers
    - trace scheduler-triggered paths (when applicable)
    - export focused subgraph
  - adds `architecture_task_suite_passed` as strict rollup gate.
  - separates `readiness_task_pass_rate` from architecture `task_pass_rate`.

M6 task-suite checks passed:
1. release gate run emits architecture task report and focused subgraph artifacts per source.
2. scorecards include architecture task summary and explicit gate flag.
3. rollup requires architecture task suite pass in addition to readiness/quality/explainability/completeness.
4. verified by runnable command: `GOCACHE=$(pwd)/.gocache ./scripts/release_gate_m6.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/release-gate-m6-post-m6-batch --clean true` with `overall_passed=true`.

### M7 Dependency/Internal Topology Product Intelligence
- New product endpoint:
  - `GET /products/topology/{graph_id}`
- Product template + catalog coverage:
  - topology template added to `/Users/developer/repos/mine/diffmind/docs/m15_query_templates.json`
  - topology question added to `/Users/developer/repos/mine/diffmind/docs/m17_question_catalog.json`
- Endpoint behavior:
  - service-scoped internal topology and external dependency summary over graph entities/edges
  - includes internal service call counts, dependency-edge counts, fan-in/fan-out, isolated services, and estimated service-cycle signal
  - supports direct HTTP usage and product template execution routing

M7 checks passed:
1. topology product endpoint is routable and authorized under product query action.
2. template execution can route to topology endpoint contract.
3. unauthenticated access is blocked; authenticated topology request returns deterministic report payload.
4. targeted regression tests and full milestone sweep remain green (`GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1` and `./scripts/milestone_sweep.sh` with summary `/Users/developer/repos/mine/diffmind/.diffmind/milestone-sweep/summary.json`).

### M8 Cross-Repo/Company Canonical Identity Product Intelligence
- New product endpoint:
  - `GET /products/company/{graph_id}`
- Product template + catalog coverage:
  - company identity template added to `/Users/developer/repos/mine/diffmind/docs/m15_query_templates.json`
  - company canonical identity question added to `/Users/developer/repos/mine/diffmind/docs/m17_question_catalog.json`
- Endpoint behavior:
  - graph-level cross-repo/canonical identity summary over company topology
  - includes canonical service/queue/database/api-host node counts and alias edge counts
  - includes cross-repo service-call edge count and top canonical clusters by member count
  - supports direct HTTP usage and product template execution routing

M8 checks passed:
1. company product endpoint is routable and authorized under product query action.
2. template execution can route to company endpoint contract.
3. unauthenticated access is blocked; authenticated company request returns deterministic report payload.
4. targeted regression tests and full milestone sweep remain green (`GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1` and `./scripts/milestone_sweep.sh` with summary `/Users/developer/repos/mine/diffmind/.diffmind/milestone-sweep/summary.json`).

### M9 Confidence/Conflict/Adjudication Surface
- New product endpoint:
  - `GET /products/trust/{graph_id}`
- New graph API subresources:
  - `GET /graphs/{graph_id}/conflicts`
  - `GET /graphs/{graph_id}/adjudications`
  - `POST /graphs/{graph_id}/adjudications`
  - `GET /graphs/{graph_id}/adjudications/summary`
- Product template + catalog coverage:
  - trust posture template added to `/Users/developer/repos/mine/diffmind/docs/m15_query_templates.json`
  - trust/conflict question added to `/Users/developer/repos/mine/diffmind/docs/m17_question_catalog.json`
- Endpoint behavior:
  - exposes confidence buckets, verification-state rollups, low-confidence counts, conflict open/resolved summaries, and adjudication decision distributions
  - persists adjudication decisions under graph-root state for reproducible review workflows
  - supports explain mode for product-layer traceability

M9 checks passed:
1. trust product endpoint is routable and authorized under product query action.
2. conflict store endpoint exposes explicit conflict records with deterministic summary counts.
3. adjudication API supports write + filtered list + summary flow with tenant/write authorization gates.
4. targeted regression tests and full milestone sweep remain green (`GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1` and `./scripts/milestone_sweep.sh` with summary `/Users/developer/repos/mine/diffmind/.diffmind/milestone-sweep/summary.json`).

### M4 Adapter Toolchain Provenance Hardening
- Strengthened adapter probe/report contract for non-builtin toolchains:
  - `tool_path`, `tool_version`, `toolchain_sha` captured in adapter plan and adapter runs.
  - replay key now includes toolchain fingerprint in addition to snapshot + adapter + extractor set.
- Added deterministic per-adapter run manifests:
  - `analyzers/runs/<adapter>.json` with immutable adapter-run attestation hash.
  - run manifest path/hash are linked from report and propagated into toolchain manifest.
- Provenance strengthening:
  - every emitted fact now includes `toolchain_sha`/`provenance_toolchain_sha` when adapter run has toolchain identity.

M4 adapter hardening checks passed:
1. adapter tests assert tool metadata + run manifest persistence for builtin/gopls/tsserver/pyright.
2. replay metadata now reflects adapter toolchain identity, improving deterministic rerun diagnosis.
3. fact-level provenance now carries adapter toolchain fingerprint for downstream auditability.
4. validation remains green:
   - `GOCACHE=$(pwd)/.gocache go test ./internal/analyzers -count=1`
   - `GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1`
   - `./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m4-adapter-toolchain-batch`

### M4 Adapter Execution Policy Hardening
- Added strict execution policy for explicitly selected adapters:
  - `extractor analyze --adapters ...` now fails if a selected adapter is unavailable.
  - opt-in degraded mode available via `--allow-missing-adapters`.
- Updated runtime contract docs to reflect:
  - strict-by-default policy for explicit adapter selections,
  - toolchain-aware replay semantics,
  - run-manifest linkage in adapter run metadata.

M4 execution-policy checks passed:
1. unavailable selected adapter fails by default with explicit error contract.
2. opt-in degraded mode preserves deterministic plan output while skipping unavailable adapter runs.
3. analyzer contract/docs stay aligned with runtime behavior.
4. validation remains green:
   - `GOCACHE=$(pwd)/.gocache go test ./internal/analyzers -count=1`
   - `./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m4-adapter-policy-batch`

### M4.3 Profile-Aware Config Resolution Hardening
- Spring resolver coverage expanded:
  - multi-document YAML parsing with profile activation support (`spring.config.activate.on-profile`, `spring.profiles`, `spring.profiles.active`).
  - profile overrides from in-document activation now participate in merged per-profile outputs.
- Placeholder resolution upgraded:
  - supports interpolated placeholders inside larger values (not only full-value placeholders),
  - unresolved placeholders are explicitly marked as `<UNRESOLVED:VAR>`,
  - emits `placeholder_vars`, `placeholder_defaults`, `placeholder_unresolved_vars`,
  - emits deterministic `placeholder_status` (`resolved`, `partially_resolved`, `unresolved`, `none`).
- New resolved-config artifact:
  - `/analyzers/resolved_config_profiles.json` emitted when Spring profile-resolved facts exist,
  - linked in analyzer report via:
    - `resolved_config_profiles_path`
    - `resolved_config_profiles_sha256`
  - includes per-profile resolved key map + code-ref linkage map for reproducible profile audits.

M4.3 checks passed:
1. profile-resolved facts and code-ref links remain present for local/prod baseline Spring cases.
2. multi-doc Spring profile activation resolves to profile-scoped facts (`stage` coverage).
3. placeholder interpolation and unresolved marker semantics are deterministic and tested.
4. resolved profile artifact is emitted and report-linked with hash attestation.
5. validation remains green:
   - `GOCACHE=$(pwd)/.gocache go test ./internal/analyzers -count=1`
   - `GOCACHE=$(pwd)/.gocache go test ./internal/httpapi -count=1`
   - `./scripts/milestone_sweep.sh --source ./corpus/fixtures/18-mixed-monorepo --out ./.diffmind/milestone-sweep-post-m4-profile-resolution-batch`

## Important Implementation Notes
1. `finalgate closeout` now auto-appends a closeout audit event to avoid false security-gate failures on fresh environments.
2. Java semantic queue extraction resolves `sendMessage(request)` / `receiveMessage(request)` queue URL from request builders when configured with `@Value` bindings.
3. Java semantic HTTP extraction now guards generic call patterns to avoid non-HTTP false positives.

## Commands Used (Reference)
```bash
# M4 evidence generation
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor scan --source ./checkout-service --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor parse --source ./checkout-service --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor analyze --source ./checkout-service --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor bundle --in .diffmind/m4-closeout/analyzers/bundle.json --out .diffmind/m4-closeout

# Full closeout (fresh no-audit copy)
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor finalgate closeout \
  --quality-gate .diffmind/e2e-m8-fixture-noaudit/quality/gate_result.json \
  --merge-quality .diffmind/e2e-m8-fixture-noaudit/service/graph/merge_quality_report.json \
  --slo .diffmind/e2e-m8-fixture-noaudit/ops/slo_report.json \
  --templates docs/m15_query_templates.json \
  --catalog docs/m17_question_catalog.json \
  --graph-index .diffmind/e2e-m8-fixture-noaudit/service/graph/index.json \
  --quality-report .diffmind/e2e-m8-fixture-noaudit/quality/report.json \
  --corpus-report .diffmind/e2e-m8-fixture-noaudit/corpus-fixtures/report.json \
  --performance-policy docs/graph_performance_baseline.md \
  --audit-root .diffmind/e2e-m8-fixture-noaudit \
  --drill-source .diffmind/e2e-m8-fixture-noaudit \
  --drill-out .diffmind/e2e-m8-fixture-noaudit/final/drills \
  --out-report .diffmind/e2e-m8-fixture-noaudit/final/readiness_report.json \
  --out-decision .diffmind/e2e-m8-fixture-noaudit/final/gate_decision.md \
  --out-milestones .diffmind/e2e-m8-fixture-noaudit/final/milestone_closure_report.json \
  --out-benchmark .diffmind/e2e-m8-fixture-noaudit/final/benchmark_evidence_report.json \
  --out-security .diffmind/e2e-m8-fixture-noaudit/final/security_validation_report.json \
  --out-ops .diffmind/e2e-m8-fixture-noaudit/final/operations_drill_report.json \
  --signers engineering,platform,security

# M10 verifier contract + queue
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor verify \
  --in .diffmind/w7-w10-verify/bundle/intelligence_bundle.json \
  --out .diffmind/w7-w10-verify

# Full runnable sweep after M10 changes
./scripts/milestone_sweep.sh \
  --source ./corpus/fixtures/18-mixed-monorepo \
  --out ./.diffmind/milestone-sweep-post-m10
```
