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

## Documentation Index

- End-to-end setup and first graph run:
  - `docs/getting_started_graph.md`
- Graph feature plan and milestones:
  - `docs/M10_service_graph_plan.md`
- Graph benchmark baseline policy:
  - `docs/graph_performance_baseline.md`
- Versioning/rollback policy:
  - `docs/versioning_policy.md`

## Module Contracts

- `internal/contracts/contracts.go`
- `internal/store/interfaces.go`
- `internal/store/contracts_assertions.go`
- `internal/facts/types.go`
- `internal/facts/validation.go`
- `internal/facts/schema.go`
