# DiffMind

Repo extractor core for generating auditable repository intelligence bundles.

## Quickstart

1. Copy env defaults:
   - `cp .env.example .env`
2. Start dependencies:
   - `make up`
3. Run DB migrations:
   - `make migrate`
4. Run CLI help:
   - `make run`
5. Run tests:
   - `make test`

## Main CLI Commands

- `go run ./cmd/extractor run`
- `go run ./cmd/extractor graph discover`
- `go run ./cmd/extractor graph build --mode multi --sources /tmp/diffmind`
- `go run ./cmd/extractor serve`
- `go run ./cmd/extractor query`
- `go run ./cmd/extractor diff`
- `go run ./cmd/extractor corpus`
- `go run ./cmd/extractor golden`
- `go run ./cmd/extractor quality evaluate`
- `go run ./cmd/extractor quality gate`
- `go run ./cmd/extractor ops slo`
- `go run ./cmd/extractor ops backup`
- `go run ./cmd/extractor ops restore`
- `go run ./cmd/extractor ops rollout`
- `go run ./cmd/extractor finalgate attest`
- Product APIs (via `serve`):
  - `POST /products/pr-review`
  - `GET /products/docs/{graph_id}`
  - `GET /products/mapper/{graph_id}`
  - `GET /products/governance/{graph_id}`

## Documentation Index

- Canonical docs index:
  - `docs/README.md`
- Current implementation audit:
  - `docs/CURRENT_IMPLEMENTATION_STATUS.md`
- Remaining work plan:
  - `docs/REMAINING_EXECUTION_PLAN.md`
- Archive record:
  - `docs/ARCHIVE_2026-02-19.md`
- Runtime/finalgate compatibility docs:
  - `docs/ontology_v2_schema.md`
  - `docs/runtime_reconciliation_runbook.md`
  - `docs/m13_security_architecture.md`
  - `docs/m14_quality_runbook.md`
  - `docs/m15_product_api_contracts.md`
  - `docs/m16_operations_runbook.md`
  - `docs/m17_completion_runbook.md`
- Runtime-critical data files:
  - `docs/graph_performance_baseline.md`
  - `docs/m15_query_templates.json`
  - `docs/m16_rollout_policy.json`
  - `docs/m17_question_catalog.json`

## Module Contracts

- `internal/contracts/contracts.go`
- `internal/store/interfaces.go`
- `internal/store/contracts_assertions.go`
- `internal/facts/types.go`
- `internal/facts/validation.go`
- `internal/facts/schema.go`
