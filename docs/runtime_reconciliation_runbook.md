# Runtime Reconciliation Runbook (Active)

## Purpose

Run runtime claim reconciliation and publish a deterministic report.

## Commands

```bash
go run ./cmd/extractor runtime plan --out .diffmind/runtime/plan.json
go run ./cmd/extractor runtime reconcile \
  --graph-id <graph_id> \
  --claims <claims.json> \
  --observations <observations.json> \
  --out .diffmind/runtime/reconcile/report.json
```

## API Endpoints

1. `GET /runtime/plan`
2. `POST /runtime/reconcile`
3. `GET /runtime/reconcile/report`
4. `GET /runtime/reconcile/compare`

Legacy detailed runbook: `docs/archive/2026-02-19-legacy/runtime_reconciliation_runbook.md`.

