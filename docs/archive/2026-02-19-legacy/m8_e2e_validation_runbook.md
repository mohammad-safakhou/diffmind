# M8 End-To-End Validation Runbook

## Goal
Run a deterministic end-to-end validation for merge-quality and closeout readiness using one service repository.

This runbook validates the full path:
1. Extraction
2. Graph build
3. Merge-quality assessment (with benchmark when expected-links are available)
4. Quality evaluate + quality gate
5. Ops SLO
6. Finalgate attestation

## Command

```bash
./scripts/e2e_m8_validation.sh \
  --source ./checkout-service \
  --clean true
```

Use explicit expected links when you have curated ground-truth:

```bash
./scripts/e2e_m8_validation.sh \
  --source ./checkout-service \
  --expect-links ./.diffmind/graph/expected_links.json \
  --clean true
```

## Behavior Notes
- If `--expect-links` is omitted, the script generates `expected_links.generated.json` from the built graph.
- If generated/provided expected-links has zero records, benchmark mode is skipped and merge quality runs on base gates only.
- Quality evaluation uses `corpus/manifest.e2e.json` (workspace-stable fixture set) with `corpus/golden/fixtures_summary.json`.
- Quality gate uses `quality/policy.e2e.json` by default. Use `--quality-policy quality/policy.json` for strict release thresholds.
- The script is fail-fast and exits non-zero on any stage failure.

## Required Successful Outputs
Under `./.diffmind/e2e-m8` (or your custom `--out` path):
- `service/graph/merge_quality_report.json`
- `quality/report.json`
- `quality/gate_result.json`
- `ops/slo_report.json`
- `final/readiness_report.json`
- `final/gate_decision.md`

## Acceptance Criteria
1. `final/readiness_report.json` has `overall_passed: true`.
2. `quality/gate_result.json` has `passed: true`.
3. `service/graph/merge_quality_report.json` has `passed: true`.
4. If benchmark is enabled, benchmark gate fields are present and passing.

## Troubleshooting
1. If extraction seems stalled, watch stage logs from the script; each command is printed before execution.
2. If benchmark fails, inspect false positive/negative samples in:
   - `service/graph/merge_quality_report.json` -> `benchmark.service_calls_service`
   - `service/graph/merge_quality_report.json` -> `benchmark.canonical_service_aliases`
3. If SLO fails, inspect `ops/slo_report.json` and validate quality pass rate plus audit event health.
