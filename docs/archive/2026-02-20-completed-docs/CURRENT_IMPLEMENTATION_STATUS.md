# Current Implementation Status (Code-Backed Audit)

Updated: 2026-02-20

Audit basis:
1. CLI modules under `internal/*`.
2. HTTP routes in `internal/httpapi/module.go`.
3. E2E harness scripts (`scripts/milestone_sweep.sh`, `scripts/release_gate_m6.sh`).
4. Final gate logic in `internal/finalgate/module.go`.

## Milestone Status (`M0-M17`)

1. `M0` Program Charter + Question Catalog: implemented.
   Evidence: `docs/m17_question_catalog.json`, finalgate readiness checks.
2. `M1` Ontology/schema baseline: implemented.
   Evidence: `docs/ontology_v2_schema.md`.
3. `M2` Evidence/provenance backbone: implemented.
   Evidence: `internal/facts/*`, `internal/audit/module.go`.
4. `M3` Extraction framework v2: implemented.
   Evidence: `internal/analyzers/module.go`, adapter runtime contracts.
5. `M4` Semantic intelligence: implemented with hardening.
   Evidence: semantic detectors across Go/JS/TS/Python/Java, Spring profile resolver.
6. `M5` Runtime/build/deploy intelligence: implemented.
   Evidence: runtime/pipeline/build detectors and `/products/runtime/:graph_id`.
7. `M6` Config + operational surface: implemented.
   Evidence: config extraction, quality gates, release gate harness.
8. `M7` Dependency/internal topology: implemented.
   Evidence: dependency detectors and `/products/topology/:graph_id`.
9. `M8` Cross-repo/company graph: implemented.
   Evidence: graph discover/build multi-source and `/products/company/:graph_id`.
10. `M9` Confidence/conflict/adjudication: implemented.
    Evidence: `/graphs/{id}/conflicts`, adjudication APIs, `/products/trust/:graph_id`.
11. `M10` Verification plane: implemented.
    Evidence: `internal/verifier/module.go`, `/verify/*` endpoints.
12. `M11` Query language + serving APIs: implemented.
    Evidence: `internal/query/module.go`, `/query/*`, template validation/execute.
13. `M12` Temporal/incremental graph: implemented.
    Evidence: `/graphs/at`, `/graphs/compare/*`, `/graphs/incremental/*`.
14. `M13` Security/compliance: implemented.
    Evidence: `internal/security/*`, `/compliance/audit*` endpoints.
15. `M14` Quality/evaluation system: implemented.
    Evidence: `internal/quality/module.go`, `/quality/*`, benchmark/finalgate integration.
16. `M15` Product layer on query: implemented.
    Evidence: `/products/*`, template and question execution endpoints.
17. `M16` Reliability/operations: implemented.
    Evidence: `internal/ops/module.go`, `/ops/*` including incidents + drill.
18. `M17` Final closeout gate: implemented.
    Evidence: `internal/finalgate/module.go`, `/final/*` including closeout.

## Verified Reality

1. The platform surface is broad and implemented end-to-end across CLI + API.
2. Milestone sweep and closeout scripts exist and run against fixture datasets.
3. Security model now supports header mode and JWT mode (`DIFFMIND_AUTH_MODE=header|jwt|auto`) with tenant/principal claim mapping.
4. Non-builtin adapters now have tool execution, deterministic tool artifacts, and tool-native semantic output merge path into analyzer facts.
5. Graph contract comparator is implemented (`graph contract`) and release gate supports contract blocking for primary-service runs.
6. `checkout-service` contract baseline is now generated from real graph extraction and checked in release-gate E2E.
7. Contract mismatch reports now include evidence samples with graph references (`graph://node/*`, `graph://edge/*`) for triage.
8. Release-gate source policy now supports a required real-repo suite with explicit rollup fields (`real_sources_total`, `fixtures_total`, `real_suite_gate_passed`, `catalogue_present`) and per-run drift deltas.
9. Final closeout now enforces closure rules with a dedicated artifact (`.diffmind/final/closure_rules_report.json`) and API retrieval endpoint (`GET /final/closure-rules`).
10. Security auth now supports IdP claim templates (`keycloak`, `entra`, `cognito`) with nested claim path resolution and explicit-override precedence.
11. Release gate now enforces per-source baseline thresholds using `quality/source_baselines.e2e.json` (or `--source-baselines`) with explicit source-level failure reasons.
12. Quality module now supports baseline calibration from release-gate summaries (`quality calibrate-baselines`) to generate source-specific threshold policy files.
13. Adapter semantic merge now supports richer tool-native schemas (`symbols`, `calls`, `external_calls`, nested package/file blocks) and normalizes them into canonical analyzer facts.
14. Adapter schema expansion now covers structured tsserver/pyright payloads including dependency/import normalization into `Dependency` facts.
15. Baseline calibration now supports multi-summary aggregation (`--summaries`) and scripted iterative tuning (`scripts/calibrate_real_repo_baselines.sh`).
16. Graph UI now supports optional advanced layout plugins (`dagre`, `ELK`) with automatic layered fallback.

## Remaining Gaps (Important)

1. No critical implementation gaps remain in the active scope. Next work is empirical hardening on larger enterprise datasets and operating cadence.
