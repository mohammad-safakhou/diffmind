# W12 Quality Harness Notes

This milestone extends the existing `quality evaluate` and `quality gate` flow with four additions:

1. Framework/version matrix scoring from corpus metadata (`framework`, `framework_version`).
2. Drift detection scoring (`drift_expected` and `drift_detected` tags or failure text containing `drift`).
3. Benchmark latency summary from corpus case durations (`p50`, `p95`, `max`, `avg`, total runtime).
4. Phase-2 runtime reconciliation contract metadata embedded in quality reports, with publish blocking disabled.

## Policy Keys

The quality gate policy supports additional threshold keys:

- `framework_matrix_pass_rate`
- `drift_precision`
- `drift_recall`
- `drift_f1`
- `benchmark_p95_ms_max`

All existing gate keys continue to work unchanged.

## Runtime Reconciliation Preparation

Runtime reconciliation is represented by a contract in
`internal/contracts/runtime_reconciliation.go`.

Current expectation:

- Reconciliation is preparation-only (`enabled=false`).
- It must not block graph publish in phase 1.
- It defines the canonical request/result model for phase-2 runtime signal ingestion.
