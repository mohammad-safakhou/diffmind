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

## Module Commands

- `go run ./cmd/extractor snapshot`
- `go run ./cmd/extractor scan`
- `go run ./cmd/extractor parse`
- `go run ./cmd/extractor analyze`
- `go run ./cmd/extractor bundle`
- `go run ./cmd/extractor query`
- `go run ./cmd/extractor diff`
- `go run ./cmd/extractor serve`
- `go run ./cmd/extractor corpus`
- `go run ./cmd/extractor golden`
- `go run ./cmd/extractor run`

## Module Contracts

Core module/store interfaces and compile-time assertions live in:

- `/Users/developer/repos/mine/diffmind/internal/contracts/contracts.go`
- `/Users/developer/repos/mine/diffmind/internal/store/interfaces.go`
- `/Users/developer/repos/mine/diffmind/internal/store/contracts_assertions.go`

## Snapshot Usage

Run snapshot on current repo worktree:

- `go run ./cmd/extractor snapshot --source . --ref HEAD --out .diffmind`
- `go run ./cmd/extractor snapshot --source . --ref HEAD --out .diffmind --persist`

Run snapshot on remote repo/ref:

- `go run ./cmd/extractor snapshot --source https://github.com/org/repo.git --ref main --out .diffmind`

Output layout:

- `.diffmind/artifacts/snapshots/<snapshot_id>/snapshot.json`
- `.diffmind/artifacts/snapshots/<snapshot_id>/inventory.json`
- `.diffmind/artifacts/blobs/<sha256>`

When `--persist` is enabled:

- Snapshot metadata and inventory rows are written to Postgres.
- Raw file blobs are written to MinIO bucket `diffmind` (configurable via `S3_BUCKET`).

## Classification Usage

Run capability scan and repo profiling:

- `go run ./cmd/extractor scan --source . --out .diffmind`

Classification report:

- `.diffmind/classification/report.json`

## Parse Usage

Run parser pipeline (structured configs + Tree-sitter + fallback):

- `go run ./cmd/extractor parse --source . --out .diffmind`

Parse report and artifacts:

- `.diffmind/parse/report.json`
- `.diffmind/parse/<snapshot_id>/<file_hash>/v1/artifact.json`

## Analyzer Usage (M5)

Run deterministic analyzers:

- `go run ./cmd/extractor analyze --source . --out .diffmind`
- `go run ./cmd/extractor analyze --source . --out .diffmind --persist`

Analyzer outputs:

- `.diffmind/analyzers/bundle.json`
- `.diffmind/analyzers/report.json`

## LLM Augmentation (M7)

Enable bounded LLM augmentation on top of deterministic analyzers:

- `OPENAI_API_KEY=... go run ./cmd/extractor analyze --source . --out .diffmind --llm-augment`

Useful flags:

- `--llm-model gpt-5-mini`
- `--llm-task augment-routes-http-config`
- `--llm-max-files 20`
- `--llm-max-chars 50000`

Audit artifacts:

- `.diffmind/llm/traces/<timestamp>.json` (prompt/response trace)

Safety rules:

- LLM facts are stored with inferred provenance (`inferred=true`, `deterministic=false`).
- Facts with missing/invalid evidence IDs are discarded.
- Final bundles still pass strict M4 validation before write/persist.

## Consolidation Usage (M6)

Generate canonical deduplicated intelligence bundle:

- `go run ./cmd/extractor bundle --in .diffmind/analyzers/bundle.json --out .diffmind`

Consolidation outputs:

- `.diffmind/bundle/intelligence_bundle.json`
- `.diffmind/bundle/report.json`

## Query Usage (M8)

Query canonical bundle entities:

- `go run ./cmd/extractor query --bundle .diffmind/bundle/intelligence_bundle.json --view endpoints --format table`
- `go run ./cmd/extractor query --bundle .diffmind/bundle/intelligence_bundle.json --view runtime --format json`

Views:

- `all`, `runtime`, `endpoints`, `config`, `external`, `pipeline`, `infra`

## Diff Usage (M8)

Diff canonical bundles between snapshots:

- `go run ./cmd/extractor diff --from .diffmind/bundle/intelligence_bundle.json --to .diffmind/bundle/intelligence_bundle.json --format table`
- `go run ./cmd/extractor diff --from <bundle_a> --to <bundle_b> --format json`

Diff compares canonical entities by `type + natural_key` and reports:

- `added`, `removed`, `changed`, `unchanged`
- per-type counters
- changed-item details (confidence/evidence/attribute changes)

## HTTP API Usage (M8 optional)

Run local HTTP API:

- `go run ./cmd/extractor serve --addr :8080 --bundle .diffmind/bundle/intelligence_bundle.json`

Endpoints:

- `GET /health`
- `GET /entities?view=endpoints`
- `GET /entities?view=runtime&bundle=/absolute/path/to/intelligence_bundle.json`
- `GET /diff?from=/absolute/path/to/bundle_a.json&to=/absolute/path/to/bundle_b.json`

## Corpus Usage (M9)

Run acceptance corpus from the 20-fixture manifest:

1. `go run ./cmd/extractor corpus --manifest corpus/manifest.fixtures.json --out .diffmind/corpus-fixtures`
2. `go run ./cmd/extractor golden --report .diffmind/corpus-fixtures/report.json --golden corpus/golden/fixtures_summary.json`

Optional self-repo smoke manifest:

- `go run ./cmd/extractor corpus --manifest corpus/manifest.example.json --out .diffmind/corpus`

Output:

- `.diffmind/corpus-fixtures/report.json`
- `.diffmind/corpus-fixtures/<case>/...` (full per-case pipeline artifacts)

## Golden Regression Usage (M9)

Verify corpus output against committed golden summary:

1. `go run ./cmd/extractor corpus --manifest corpus/manifest.fixtures.json --out .diffmind/corpus-fixtures`
2. `go run ./cmd/extractor golden --report .diffmind/corpus-fixtures/report.json --golden corpus/golden/fixtures_summary.json`

Update golden summary intentionally:

- `go run ./cmd/extractor golden --report .diffmind/corpus-fixtures/report.json --golden corpus/golden/fixtures_summary.json --update`

## Pipeline Reliability and Observability (M9)

Run full pipeline with retry/resume controls:

- `go run ./cmd/extractor run --source . --ref HEAD --out .diffmind --retries 2 --retry-delay-ms 300 --resume`
- `go run ./cmd/extractor run --source . --ref HEAD --out .diffmind --no-resume`

Run report artifact:

- `.diffmind/run/report.json`

Report includes:

- per-stage status
- attempts per stage
- stage duration in milliseconds
- error taxonomy (`timeout`, `transient_network`, `filesystem`, `data_contract`, `unknown`)

## Benchmark Suite (M9)

Run benchmark profiles (`small`, `medium`, `large`):

- `go test ./internal/benchmark -bench . -benchmem`
- or `make bench`

## Rollback and Compatibility Policy (M9)

- `/Users/developer/repos/mine/diffmind/docs/versioning_policy.md`

## Fact Contract (M4)

Core contracts live in:

- `/Users/developer/repos/mine/diffmind/internal/facts/types.go`
- `/Users/developer/repos/mine/diffmind/internal/facts/validation.go`
- `/Users/developer/repos/mine/diffmind/internal/facts/schema.go`

Persistence schema:

- `/Users/developer/repos/mine/diffmind/migrations/000002_facts.sql`

Validation rule:

- Facts without evidence are rejected (`ErrMissingEvidence`).
