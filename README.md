# DiffMind

Repo extractor core for generating auditable repository intelligence bundles.

## Quickstart

1. Install core tooling + language servers:
   - `make setup`
2. Verify environment:
   - `make doctor`
3. Run full extraction + graph build on a service:
   - `make run-full SOURCE=/absolute/path/to/service OUT=$(pwd)/.diffmind`
   - Optional multi-adapter run: `ADAPTERS=builtin,gopls,tsserver,pyright,jdtls make run-full SOURCE=/absolute/path/to/service OUT=$(pwd)/.diffmind`
4. Start API/UI server:
   - `make serve-out OUT=$(pwd)/.diffmind ADDR=:8080`
5. Optional strict check (fails if any LSP is missing):
   - `make doctor-strict`

### Setup Targets

- `make setup-core`: install core dependencies (`go`, `jq`, `ripgrep`, `node`, `java`)
- `make setup-lsp`: install LSP tooling (`gopls`, `tsserver`, `pyright-langserver`, and `jdtls` where available)
- `make setup`: run both setup phases and verify
- `make run-e2e`: default end-to-end run pipeline script
- `make run-full`: full semantic run with multi-adapter planning

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
- Final execution plan:
  - `docs/FINAL_EXECUTION_PLAN.md`
- Completed docs archive:
  - `docs/archive/2026-02-20-completed-docs/`
- Legacy archive:
  - `docs/archive/2026-02-19-legacy/`
- Runtime/finalgate compatibility docs:
  - `docs/ontology_v2_schema.md`
  - `docs/runtime_reconciliation_runbook.md`
  - `docs/archive/2026-02-20-completed-docs/m13_security_architecture.md`
  - `docs/archive/2026-02-20-completed-docs/m14_quality_runbook.md`
  - `docs/archive/2026-02-20-completed-docs/m15_product_api_contracts.md`
  - `docs/archive/2026-02-20-completed-docs/m16_operations_runbook.md`
  - `docs/archive/2026-02-20-completed-docs/m17_completion_runbook.md`
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
