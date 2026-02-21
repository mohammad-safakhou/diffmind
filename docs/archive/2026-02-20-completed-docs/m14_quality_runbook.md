# M14 Quality Runbook (Active)

## Evaluate Quality

```bash
go run ./cmd/extractor quality evaluate \
  --corpus .diffmind/corpus-fixtures/report.json \
  --golden .diffmind/golden/report.json \
  --graph-index .diffmind/service/graph/index.json \
  --merge-quality-auto \
  --out .diffmind/quality/report.json \
  --dashboard .diffmind/quality/dashboard.md \
  --triage .diffmind/quality/triage.md
```

## Apply Quality Gate

```bash
go run ./cmd/extractor quality gate \
  --report .diffmind/quality/report.json \
  --policy quality/policy.json \
  --out .diffmind/quality/gate_result.json
```

## Release Gate (M6) With Source Baselines

```bash
bash scripts/release_gate_m6.sh \
  --source ./checkout-service \
  --source-baselines quality/source_baselines.e2e.json \
  --require-real-suite true \
  --strict true
```

## Calibrate Source Baselines From Release-Gate Summary

```bash
go run ./cmd/extractor quality calibrate-baselines \
  --summary .diffmind/release-gate-m6/summary.json \
  --out quality/source_baselines.e2e.json \
  --min-samples 2
```

## Calibrate From Multiple Historical Summaries

```bash
go run ./cmd/extractor quality calibrate-baselines \
  --summary .diffmind/release-gate-m6/summary.json \
  --summaries .diffmind/release-gate-history/run-001/summary.json,.diffmind/release-gate-history/run-002/summary.json \
  --out quality/source_baselines.e2e.json \
  --min-samples 2
```

## Automated Calibration Script

```bash
bash scripts/calibrate_real_repo_baselines.sh \
  --summary-glob ".diffmind/release-gate-history/*/summary.json" \
  --out quality/source_baselines.e2e.json \
  --min-samples 2
```

Legacy detailed runbook: `docs/archive/2026-02-19-legacy/m14_quality_runbook.md`.
