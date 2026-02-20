# M14 Quality And Evaluation Runbook

## Goal
Continuously measure extraction quality and block releases on critical regressions.

## Commands
1. Evaluate corpus output against golden baseline:

```bash
extractor quality evaluate \
  --corpus .diffmind/corpus/report.json \
  --golden corpus/golden/summary.json \
  --merge-quality .diffmind/graph/merge_quality_report.json \
  --graph-index .diffmind/graph/index.json \
  --merge-quality-expect-links .diffmind/graph/expected_links.json \
  --merge-quality-auto \
  --out .diffmind/quality/report.json \
  --dashboard .diffmind/quality/dashboard.md \
  --triage .diffmind/quality/triage.md
```

Note: with `--merge-quality-auto` and `--merge-quality-expect-links`, evaluation refreshes stale merge-quality reports that are missing benchmark sections.

2. Enforce release gate policy:

```bash
extractor quality gate \
  --report .diffmind/quality/report.json \
  --policy quality/policy.json \
  --out .diffmind/quality/gate_result.json
```

Policy now includes matrix/drift/benchmark thresholds and merge-linkage gates in addition to base precision/recall:
`framework_matrix_pass_rate`, `drift_precision`, `drift_recall`, `drift_f1`, `benchmark_p95_ms_max`, `merge_quality_required`, `merge_quality_benchmark_required`, `merge_quality_linkage_precision`, `merge_quality_linkage_recall`, `merge_quality_identity_precision`, `merge_quality_identity_recall`.

`graph assess --expect-links` benchmark now supports both:
- `service_calls_service` linkage expectations
- `canonical_service_aliases` identity expectations

## CI/CD Integration
- Add the gate command as a required check.
- Any non-zero exit from `quality gate` blocks release.

## HTTP API Workflow
Quality flow can be executed over the API for UI and enterprise orchestration:

```bash
# Evaluate + generate dashboard/triage
curl -s -X POST http://localhost:8080/quality/evaluate \
  -H 'Content-Type: application/json' \
  -H 'X-DiffMind-Tenant: default' \
  -H 'X-DiffMind-Principal: quality-bot' \
  -H 'X-DiffMind-Roles: tenant_admin' \
  -H 'X-DiffMind-Scopes: graph:write' \
  -d '{
    "corpus_path": ".diffmind/corpus/report.json",
    "golden_path": "corpus/golden/summary.json",
    "merge_quality_path": ".diffmind/graph/merge_quality_report.json",
    "graph_index_path": ".diffmind/graph/index.json",
    "merge_quality_expect_links_path": ".diffmind/graph/expected_links.json",
    "merge_quality_auto": true,
    "out_path": ".diffmind/quality/report.json",
    "dashboard_path": ".diffmind/quality/dashboard.md",
    "triage_path": ".diffmind/quality/triage.md"
  }'

# Gate quality (returns overall_passed=false + gate_error when thresholds fail)
curl -s -X POST http://localhost:8080/quality/gate \
  -H 'Content-Type: application/json' \
  -H 'X-DiffMind-Tenant: default' \
  -H 'X-DiffMind-Principal: quality-bot' \
  -H 'X-DiffMind-Roles: tenant_admin' \
  -H 'X-DiffMind-Scopes: graph:write' \
  -d '{
    "report_path": ".diffmind/quality/report.json",
    "policy_path": "quality/policy.json",
    "out_path": ".diffmind/quality/gate_result.json"
  }'

# Read generated artifacts
curl -s http://localhost:8080/quality/report -H 'X-DiffMind-Tenant: default' -H 'X-DiffMind-Principal: qa' -H 'X-DiffMind-Roles: analyst' -H 'X-DiffMind-Scopes: graph:read'
curl -s http://localhost:8080/quality/dashboard -H 'X-DiffMind-Tenant: default' -H 'X-DiffMind-Principal: qa' -H 'X-DiffMind-Roles: analyst' -H 'X-DiffMind-Scopes: graph:read'
curl -s http://localhost:8080/quality/triage -H 'X-DiffMind-Tenant: default' -H 'X-DiffMind-Principal: qa' -H 'X-DiffMind-Roles: analyst' -H 'X-DiffMind-Scopes: graph:read'
curl -s http://localhost:8080/quality/gate -H 'X-DiffMind-Tenant: default' -H 'X-DiffMind-Principal: qa' -H 'X-DiffMind-Roles: analyst' -H 'X-DiffMind-Scopes: graph:read'
```

## Triage Process
1. Open `.diffmind/quality/triage.md` and prioritize `sev1` regressions first.
2. Re-run failing corpus cases to confirm reproducibility.
3. Compare entity-count deltas vs golden baseline.
4. Patch parser/analyzer/consolidation and re-run `quality evaluate` + `quality gate`.
5. Release only when `quality gate` passes and `sev1` regressions are zero.

## Required Artifacts
- `.diffmind/quality/report.json`
- `.diffmind/quality/dashboard.md`
- `.diffmind/quality/triage.md`
- `.diffmind/quality/gate_result.json`
