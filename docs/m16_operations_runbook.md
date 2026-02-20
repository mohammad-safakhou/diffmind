# M16 Operations Runbook (Active)

## CLI Operations

```bash
go run ./cmd/extractor ops slo \
  --audit-root .diffmind \
  --quality .diffmind/quality/report.json \
  --out .diffmind/ops/slo_report.json

go run ./cmd/extractor ops backup --source .diffmind --out .diffmind/ops/backup.tar.gz
go run ./cmd/extractor ops restore --archive .diffmind/ops/backup.tar.gz --target .diffmind-restore
go run ./cmd/extractor ops rollout --component extractor --candidate vNEXT --current vCURRENT --out .diffmind/ops/rollout_plan.json
```

## API Operations

1. `GET /ops/metrics`
2. `GET /ops/slo`
3. `POST /ops/slo/evaluate`
4. `GET /ops/incidents`
5. `GET /ops/incidents/:id`
6. `GET /ops/rollout-policy`
7. `POST /ops/backup`
8. `POST /ops/restore`
9. `POST /ops/rollout`
10. `POST /ops/drill`

Rollout policy file: `docs/m16_rollout_policy.json`.

Legacy detailed runbook: `docs/archive/2026-02-19-legacy/m16_operations_runbook.md`.

