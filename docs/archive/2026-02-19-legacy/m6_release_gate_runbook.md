# M6 Release Gate Runbook

This runbook defines the M6 executable release gate harness for enterprise validation.

## Purpose

Run a reproducible, multi-repository E2E gate that outputs a single scorecard with:
1. accuracy
2. completeness
3. explainability
4. task pass-rate

## Command

```bash
./scripts/release_gate_m6.sh \
  --source ./checkout-service \
  --source ./corpus/fixtures/01-go-gin-service \
  --source ./corpus/fixtures/03-node-express-api \
  --out ./.diffmind/release-gate-m6 \
  --clean true
```

You can also rely on default sources (three fixture repos):

```bash
./scripts/release_gate_m6.sh --clean true
```

## Artifacts

Under `--out` (default `./.diffmind/release-gate-m6`):

1. `runs/<source>/...`:
   per-source full E2E artifacts from `scripts/e2e_m8_validation.sh`
2. `scorecards/<source>.json`:
   per-source scorecard
3. `scorecards/all_scorecards.json`:
   aggregated per-source scorecards
4. `runs/<source>/tasks/architecture_tasks.json`:
   per-source architecture task evaluation report
5. `runs/<source>/tasks/focused_subgraph.json`:
   exported focused subgraph used by task validation
6. `summary.json`:
   release gate rollup with overall pass/fail
7. `summary.md`:
   human-readable summary

## Scorecard Contract

Per source:
1. `scorecard.accuracy`: pass_rate, precision, recall, f1
2. `scorecard.completeness`:
   node/edge counts and section presence check (`exposure`, `logic`, `dependencies`)
3. `scorecard.explainability`:
   final readiness checks for explainability and traceability
4. `scorecard.architecture_tasks`:
   executable architecture task suite (`find_exposures`, `trace_endpoint_to_dependencies`,
   `identify_queue_consumers_publishers`, `trace_scheduler_trigger_paths`, `export_focused_subgraph`)
   with `summary.pass_rate`
5. `scorecard.readiness_task_pass_rate`:
   pass ratio across readiness task checks:
   - `question_catalog_coverage_100`
   - `question_catalog_api_coverage_100`
   - `explainability_traceability_100`
   - `graph_traceability_coverage_100`
6. `scorecard.task_pass_rate`:
   architecture task pass rate (gate-critical)

Rollup passes only when all runs satisfy readiness, quality gate, architecture task suite, explainability, completeness section presence, and task pass-rate `== 1`.

## Operator Notes

1. API contract tests run first by default:
   `go test ./internal/httpapi -run 'TestProductEndpoints|TestMergeQualityEndpoints|TestQualityEndpoints' -count=1`
2. Use `--api-contract-tests false` only for emergency triage runs.
3. Use `--strict false` to collect artifacts without failing CI.
