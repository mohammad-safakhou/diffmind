# M17 Final Completion Runbook

## Objective
Validate all mandatory program gates and emit final approval artifacts.

## Command

```bash
extractor finalgate attest \
  --quality-gate .diffmind/quality/gate_result.json \
  --slo .diffmind/ops/slo_report.json \
  --templates docs/m15_query_templates.json \
  --catalog docs/m17_question_catalog.json \
  --graph-index .diffmind/graph/index.json \
  --out-report .diffmind/final/readiness_report.json \
  --out-decision .diffmind/final/gate_decision.md \
  --signers engineering,platform,security
```

## Required Outputs
- `.diffmind/final/readiness_report.json`
- `.diffmind/final/gate_decision.md`

## Approval Rule
- Final state-of-the-art completion is approved only when:
  1. all checks pass in readiness report,
  2. gate decision is `APPROVE`,
  3. signatures include engineering, platform, and security.
