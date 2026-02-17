# M14 Quality And Evaluation Runbook

## Goal
Continuously measure extraction quality and block releases on critical regressions.

## Commands
1. Evaluate corpus output against golden baseline:

```bash
extractor quality evaluate \
  --corpus .diffmind/corpus/report.json \
  --golden corpus/golden/summary.json \
  --out .diffmind/quality/report.json \
  --dashboard .diffmind/quality/dashboard.md \
  --triage .diffmind/quality/triage.md
```

2. Enforce release gate policy:

```bash
extractor quality gate \
  --report .diffmind/quality/report.json \
  --policy quality/policy.json \
  --out .diffmind/quality/gate_result.json
```

## CI/CD Integration
- Add the gate command as a required check.
- Any non-zero exit from `quality gate` blocks release.

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
