# M17 Final Completion Runbook (Active)

## Attestation

```bash
go run ./cmd/extractor finalgate attest \
  --quality-gate .diffmind/quality/gate_result.json \
  --merge-quality .diffmind/service/graph/merge_quality_report.json \
  --slo .diffmind/ops/slo_report.json \
  --templates docs/m15_query_templates.json \
  --catalog docs/m17_question_catalog.json \
  --graph-index .diffmind/service/graph/index.json \
  --out-report .diffmind/final/readiness_report.json \
  --out-decision .diffmind/final/gate_decision.md \
  --signers engineering,platform,security
```

## Full Closeout

```bash
go run ./cmd/extractor finalgate closeout \
  --quality-gate .diffmind/quality/gate_result.json \
  --merge-quality .diffmind/service/graph/merge_quality_report.json \
  --slo .diffmind/ops/slo_report.json \
  --templates docs/m15_query_templates.json \
  --catalog docs/m17_question_catalog.json \
  --graph-index .diffmind/service/graph/index.json \
  --contract-report .diffmind/graph/contract_report.json \
  --quality-report .diffmind/quality/report.json \
  --corpus-report .diffmind/corpus-fixtures/report.json \
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
  --out-closure-rules .diffmind/final/closure_rules_report.json \
  --signers engineering,platform,security
```

## API Endpoints

1. `POST /final/attest`
2. `POST /final/closeout`
3. `GET /final/readiness`
4. `GET /final/decision`
5. `GET /final/milestones`
6. `GET /final/benchmark`
7. `GET /final/security`
8. `GET /final/ops`
9. `GET /final/closure-rules`

Legacy detailed runbook: `docs/archive/2026-02-19-legacy/m17_completion_runbook.md`.
