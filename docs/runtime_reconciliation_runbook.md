# Runtime Reconciliation Runbook

Runtime reconciliation is a phase-2 preparation flow and is not publish-blocking.

## 1) Generate plan artifact

```bash
extractor runtime plan --out .diffmind/runtime/plan.json
```

## 2) Run dry-run reconciliation

```bash
extractor runtime reconcile \
  --graph-id graph-123 \
  --claims .diffmind/runtime/claims.json \
  --observations .diffmind/runtime/observations.json \
  --out .diffmind/runtime/reconcile_result.json

## 3) Generate claims from graph (HTTP API)

```bash
curl -s \
  -H "X-DiffMind-Tenant: default" \
  -H "X-DiffMind-Principal: user" \
  -H "X-DiffMind-Roles: analyst" \
  -H "X-DiffMind-Scopes: graph:read" \
  "http://localhost:8080/runtime/claims/graph-123?sections=exposure,dependencies"
```
```

## Input contracts

- Claims: array of `RuntimeClaim` objects (graph claim candidates).
- Observations: array of `RuntimeObservation` objects (runtime telemetry signals).

## Output contract

`RuntimeReconciliationResult` includes:

- `confirmed`
- `contradicted`
- `runtime_only_unmapped`
- `needs_review`

## History APIs

Runtime reconcile runs are persisted under graph artifacts and queryable via HTTP:

- `GET /runtime/reconcile` lists reconcile runs (supports `limit` and `before` cursor).
- `GET /runtime/reconcile/{reconcile_id}` returns full request+result for one run.
- `GET /runtime/reconcile/compare?from=<id>&to=<id>` compares runtime run outcomes.
- `GET /runtime/reconcile/report?graph_id=<id>&from=<RFC3339>&to=<RFC3339>` returns aggregate runtime quality metrics and top graphs.
- `DELETE /runtime/reconcile/{reconcile_id}` deletes one run.
- `DELETE /runtime/reconcile?keep_latest=20` prunes old runs.

Current policy:

- reconciliation support is enabled for analysis only.
- graph publish remains controlled by static quality/verifier gates.
