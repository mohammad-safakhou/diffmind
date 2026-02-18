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

## Full Closeout Command (All Remaining Tasks)

```bash
extractor finalgate closeout \
  --quality-gate .diffmind/quality/gate_result.json \
  --slo .diffmind/ops/slo_report.json \
  --templates docs/m15_query_templates.json \
  --catalog docs/m17_question_catalog.json \
  --graph-index .diffmind/graph/index.json \
  --quality-report .diffmind/quality/report.json \
  --corpus-report .diffmind/corpus/report.json \
  --performance-policy docs/graph_performance_baseline.md \
  --audit-root .diffmind \
  --drill-source .diffmind \
  --drill-out .diffmind/final/drills \
  --out-report .diffmind/final/readiness_report.json \
  --out-decision .diffmind/final/gate_decision.md \
  --out-milestones .diffmind/final/milestone_closure_report.json \
  --out-benchmark .diffmind/final/benchmark_evidence_report.json \
  --out-security .diffmind/final/security_validation_report.json \
  --out-ops .diffmind/final/operations_drill_report.json \
  --signers engineering,platform,security
```

## Required Outputs
- `.diffmind/final/readiness_report.json`
- `.diffmind/final/gate_decision.md`

## HTTP API (Ops/UI)
- `POST /final/attest` runs attestation and returns readiness payload.
- `GET /final/readiness` reads readiness report (`?path=...` optional override).
- `GET /final/decision` reads gate decision markdown (`?path=...` optional override).

## Approval Rule
- Final state-of-the-art completion is approved only when:
  1. all checks pass in readiness report,
  2. gate decision is `APPROVE`,
  3. signatures include engineering, platform, and security.
  4. question catalog endpoints are fully covered by M15 query templates.
  5. SLO artifact runtime-quality gate (when present) is passing.
