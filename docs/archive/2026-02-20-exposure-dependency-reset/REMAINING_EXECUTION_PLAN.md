# Remaining Execution Plan (Post-Audit)

Updated: 2026-02-20

This is the single active plan for what is left.

## Priority 1: Ground-Truth Contract Gate For Primary Service (Completed 2026-02-19)

Completed:
1. Created `docs/contracts/checkout-service.expected_graph_contract.json` from real extraction output.
2. Added automated contract compare command (`graph contract`) and release gate blocking on critical mismatch.
3. Added mismatch/observed evidence samples with graph references in contract reports.

Validated:
1. Critical surface thresholds met for `checkout-service` (`contract_gate_passed=true`).
2. Contract report is generated in release-gate runs at `runs/<source>/tasks/graph_contract_report.json`.

## Priority 2: Upgrade Release Gate Inputs (Completed 2026-02-20)

Completed:
1. Updated release gate defaults to prefer real repos first (`checkout-service`, `../sample-service`, `../inventory-service`) with fixtures retained as smoke.
2. Added required real-suite policy controls: `--require-real-suite`, `--min-real-sources`.
3. Added rollup suite metrics and gate fields in summary (`real_sources_total`, `real_sources_passed`, `fixtures_total`, `catalogue_present`, `real_suite_gate_passed`).
4. Added per-run drift deltas in scorecards (`precision_delta`, `recall_delta`, `f1_delta`, `node_count_delta`, `edge_count_delta`).

Validated:
1. Fixtures-only suite fails strict gate with `real_suite_gate_passed=false`.
2. Three-real-repo suite reports `real_suite_gate_passed=true` and preserves per-repo pass/fail visibility.

## Priority 3: Dense Graph UX/Operator Workflow Hardening (Completed 2026-02-20)

Completed:
1. Added layered architecture layout path in graph UI.
2. Added first-class operator workflow tasks in UI:
   expose -> trace -> dependency map -> focused export.
3. Added task telemetry ingest and retrieval (`/ops/ui-telemetry`) with summaries.
4. Added UI-side operator SLA validator for task coverage, duration (`<= 60s` avg per core task), and dead-end ratio.
5. Added dense-graph performance hardening:
   layout caching + dense-edge decimation while zoomed out.

Validation:
1. UI script syntax validation passes (`node --check internal/httpapi/ui/app.js`).
2. HTTP API regressions pass with telemetry coverage (`go test ./internal/httpapi`).
3. UI contains SLA control and workflow controls (`TestUIIndexIsServed`).

## Priority 4: Enterprise Identity Integration (Completed 2026-02-20)

Completed:
1. Added JWT auth mode and mixed mode in security context extraction:
   `DIFFMIND_AUTH_MODE=header|jwt|auto`.
2. Added JWT claim mapping for tenant/principal/roles/scopes/attrs via `DIFFMIND_AUTH_*` env configuration.
3. Added JWT signature verification for HS256 and RS256.
4. Preserved header mode for local/dev while allowing strict JWT enforcement in production-style runs.

Validated:
1. Protected endpoints reject unsigned/invalid tokens in JWT mode (`TestJWTAuthModeForProtectedEndpoints`).
2. Security package tests validate JWT context extraction and auth-mode behavior (`internal/security/jwt_test.go`).
3. Existing HTTP API auth coverage remains green under default header mode.

## Priority 5: Closure Rules (Completed 2026-02-20)

Completed:
1. Added closure-rules evaluation to `finalgate closeout` with explicit hard gates:
   contract report + evidence sampling + product validation + ops validation.
2. Added closeout CLI inputs/outputs:
   `--contract-report`, `--out-closure-rules`.
3. Added closure-rules artifact emission:
   `.diffmind/final/closure_rules_report.json`.
4. Added HTTP API support for closure-rules output in closeout responses and read endpoint:
   `GET /final/closure-rules`.

Validated:
1. `go test ./internal/finalgate ./internal/httpapi -count=1` passes.
2. Closeout succeeds with valid contract evidence (`graph://...`) and fails when evidence is missing/invalid.

## Priority 6: OIDC Discovery + JWKS Rotation (Completed 2026-02-20)

Completed:
1. Added OIDC/JWKS auth config in security runtime:
   `DIFFMIND_AUTH_JWT_OIDC_ISSUER`, `DIFFMIND_AUTH_JWT_JWKS_URL`, `DIFFMIND_AUTH_JWT_JWKS_CACHE_SECONDS`.
2. Extended RS256 verification path to resolve public keys from JWKS (direct URL or OIDC discovery).
3. Added JWKS cache with TTL and `kid`-aware key selection.
4. Preserved static RS256 PEM/path fallback for offline/local operation.

Validated:
1. `go test ./internal/security -count=1` passes.
2. `go test ./internal/httpapi -run TestJWTAuthModeForProtectedEndpoints -count=1` passes.
3. `go test ./internal/security ./internal/httpapi -count=1` passes.

## Priority 7: IdP Claim Templates + Rollout Profiles (Completed 2026-02-20)

Completed:
1. Added profile-based claim mapping presets with env selector:
   `DIFFMIND_AUTH_PROFILE=custom|keycloak|entra|cognito`.
2. Added nested JWT claim path resolution (e.g. `realm_access.roles`) for IdP-native token schemas.
3. Added scope parsing compatibility for both comma-separated and space-separated forms (`scope`, `scp`).
4. Ensured explicit claim env overrides continue to take precedence over profile defaults.

Validated:
1. `go test ./internal/security -count=1` passes, including profile and nested-claim tests.
2. `go test ./internal/httpapi -run TestJWTAuthModeForProtectedEndpoints -count=1` passes.
3. `go test ./internal/security ./internal/httpapi -count=1` passes.

## Priority 8: Per-Repo Baseline Threshold Hardening (Completed 2026-02-20)

Completed:
1. Added release-gate per-source baseline policy support via `--source-baselines`.
2. Added default baseline policy file at `quality/source_baselines.e2e.json`.
3. Added per-run baseline evaluation artifact and gate fields in scorecards:
   `source_baseline_applicable`, `source_baseline_passed`, `source_baseline_failures`.
4. Added aggregate rollup gate in summary:
   `rollup.source_baselines_passed`.
5. Included source baseline gate in strict `overall_passed` decision path.

Validated:
1. `bash -n scripts/release_gate_m6.sh` passes.
2. Non-strict single-fixture run emits baseline failures and `overall_passed=false` in summary.
3. Strict single-fixture run exits non-zero when baseline thresholds are violated.

## Priority 9: Baseline Calibration Workflow (Completed 2026-02-20)

Completed:
1. Added `quality calibrate-baselines` command to generate per-source baseline policy from release-gate runs.
2. Added calibration controls:
   `--summary`, `--out`, `--min-samples`, `--include-fixtures`, `--percentile`, margin/floor flags.
3. Added policy metadata output and deterministic default+source threshold emission.
4. Added quality unit tests for real-only calibration and fixture-included calibration modes.

Validated:
1. `go test ./internal/quality -count=1` passes.
2. `go test ./internal/httpapi -run 'TestQualityEndpoints|TestProductEndpoints|TestMergeQualityEndpoints' -count=1` passes.
3. CLI E2E run produces calibrated policy:
   `go run ./cmd/extractor quality calibrate-baselines --summary /tmp/diffmind-release-gate-check/summary.json --out /tmp/diffmind-calibrated-baselines.json --include-fixtures`.

## Priority 10: Adapter Deep Semantic Schema Merge (Completed 2026-02-20)

Completed:
1. Extended adapter semantic merge ingestion to support richer tool-native schemas beyond `facts[]`.
2. Added normalization for structured semantic payloads:
   `symbols`, `calls`, `external_calls`, and nested `packages[].files[]` semantic blocks.
3. Added adapter-language-aware normalization so structured payloads map into canonical fact types:
   `CodeSymbol`, `CodeCall`, `ExternalCall`.
4. Added structured semantic merge regression test for gopls adapter output.

Validated:
1. `go test ./internal/analyzers -count=1` passes.

## Priority 11: Adapter Schema Expansion (tsserver/pyright) (Completed 2026-02-20)

Completed:
1. Expanded structured semantic normalization to include dependency/import records in addition to symbols/calls/external calls.
2. Added tsserver structured semantic test coverage for:
   `CodeSymbol`, `CodeCall`, `ExternalCall`, `Dependency`.
3. Added pyright structured semantic test coverage for:
   `CodeSymbol`, `CodeCall`, `ExternalCall`, `Dependency`.

Validated:
1. `go test ./internal/analyzers -count=1` passes.

## Priority 12: Enterprise Baseline Tuning Workflow (Completed 2026-02-20)

Completed:
1. Extended `quality calibrate-baselines` to merge multiple release-gate summaries via `--summaries`.
2. Added iterative calibration helper script:
   `scripts/calibrate_real_repo_baselines.sh`.
3. Added multi-summary calibration tests and metadata coverage.

Validated:
1. `go test ./internal/quality -count=1` passes.
2. `scripts/calibrate_real_repo_baselines.sh --summary-glob '/tmp/diffmind-release-gate-check/summary.json' --out /tmp/diffmind-calibrated-baselines-from-script.json --min-samples 1 --include-fixtures true` succeeds.

## Priority 13: Optional Advanced Layout Engine Integration (Completed 2026-02-20)

Completed:
1. Added optional UI layout modes:
   `dagre_engine` and `elk_engine`.
2. Added plugin-aware layout hooks:
   uses `window.dagre`/`window.graphlib` and `window.ELK` when present.
3. Added automatic layered-architecture fallback with explicit UI note when plugins are missing or still computing.

Validated:
1. `node --check internal/httpapi/ui/app.js` passes.
2. `go test ./internal/httpapi -count=1` passes.
