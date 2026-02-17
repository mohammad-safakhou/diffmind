# M16 Reliability And Operations Runbook

## Operational SLOs
- Correctness-sensitive API SLO: `>= 99.9%`
- Data integrity incidents: `0` critical incidents
- Rollback reliability: `100%` restore success in tested drill

## Runtime Observability
Use HTTP ops endpoints (requires compliance/audit access):
- `GET /ops/metrics`
- `GET /ops/slo`

## CLI Operations
1. Evaluate SLO status from audit and quality artifacts:

```bash
extractor ops slo \
  --audit-root .diffmind \
  --quality .diffmind/quality/report.json \
  --out .diffmind/ops/slo_report.json
```

2. Create operational backup:

```bash
extractor ops backup \
  --source .diffmind \
  --out .diffmind/ops/backup-$(date +%s).tar.gz
```

3. Restore backup (rollback drill or incident recovery):

```bash
extractor ops restore \
  --archive .diffmind/ops/backup-<ts>.tar.gz \
  --target .diffmind-restore
```

4. Generate rollout plan with explicit rollback version:

```bash
extractor ops rollout \
  --component extractor \
  --candidate vNEXT \
  --current vCURRENT \
  --out .diffmind/ops/rollout_plan.json
```

## Incident/DR Procedure
1. Freeze rollout.
2. Confirm gate failures from `ops slo` and `quality gate`.
3. Restore last known good backup.
4. Roll back to previous stable version in rollout plan.
5. Re-run SLO and quality gates before traffic recovery.
