# DiffMind

DiffMind is the deterministic service-context extractor. It scans one source
repository and writes a DiffMind protocol service document that DiffMind can ingest into a
company graph.

DiffMind is deterministic-only. It does not use OpenCode, LLM prompts, model
providers, snapshots, retry prompts, or legacy `diffmind.yaml` ingestion.

## What DiffMind Produces

For each run, DiffMind creates a run directory under `~/.diffmind/runs` by
default:

```text
~/.diffmind/runs/<run_id>/
  run_manifest.json
  .diffmind/context/service.json
  diffmind.yaml
```

The canonical generated artifact is:

```text
.diffmind/context/service.json
```

That file uses DiffMind protocol schema `diffmind.service.v1` and contains service metadata,
objects, observations, evidence, and flows.

## Requirements

- Go 1.26.2 or newer compatible toolchain.
- Node.js and npm only if you want to rebuild the optional DiffMind dashboard.
- Docker is useful for SCIP/indexer paths used by some repositories.
- A source repository to analyze.

The local development checkout expects these repositories as siblings:

```text
eyes/
  protocol/
  diffmind/
  diffmind/
```

`diffmind/go.mod` uses:

```text
replace github.com/mohammad-safakhou/protocol => ../protocol
```

If your checkout layout is different, update the `replace` path or publish DiffMind protocol
as a normal module dependency.

## Build And Test

```bash
cd /path/to/eyes/diffmind
go test ./...
go build -o ./bin/diffmind ./cmd/diffmind
```

Optional UI bundle:

```bash
cd /path/to/eyes/diffmind/internal/ui/web
npm install
npm run build
```

## Run DiffMind Directly

```bash
cd /path/to/eyes/diffmind
go run ./cmd/diffmind run \
  --repo /absolute/path/to/service-repo \
  --workers 8 \
  --min-confidence 0.70
```

With a built binary:

```bash
./bin/diffmind run \
  --repo /absolute/path/to/service-repo \
  --out ~/.diffmind/runs \
  --workers 8 \
  --min-confidence 0.70
```

Useful flags:

| Flag | Meaning |
| --- | --- |
| `--repo` | Absolute path to the source repository. Required. |
| `--config` | Optional central DiffMind JSON config. Usually not needed. |
| `--out` | Artifact base directory. Defaults to `~/.diffmind/runs`. |
| `--workers` | Parallel parser/index worker count. |
| `--min-confidence` | Minimum accepted confidence, usually `0.70`. |
| `--verbose` | Debug logging. |
| `--trace` | Very noisy trace logging. |
| `--log-file` | Append logs to a file. |

Validate a run:

```bash
go run ./cmd/diffmind validate --run <run_id>
```

List runs:

```bash
go run ./cmd/diffmind list-runs
```

## Repository Hints

Manual hints live in the analyzed repository as:

```text
diffmind-configuration.yaml
```

This file is optional. Use it only when source code/config does not contain
enough information to resolve company-specific conventions.

Minimal example:

```yaml
schema: diffmind.config.v1

service:
  id: orders-api
  name: orders-api
  team: commerce
  domain: checkout
  criticality: high

aliases:
  services:
    checkout-service:
      - catalogue_api
      - checkout-service.default.svc.cluster.local
  resources:
    orders-db:
      - ORDERS_DATABASE_URL
      - orders_postgres

http_targets:
  - id: catalogue-api
    service_ref: service.checkout-service
    client_class: CatalogueClient
    config_key: services.catalogue.url

resource_patterns:
  - id: orders-postgres
    kind: database
    platform: postgres
    resource_ref: db.orders-db
    config_key: spring.datasource.url

detectors:
  disabled: []
```

Do not use `detectors.enabled` for normal operation. DiffMind runs all relevant
detectors by language by default. Disable only a known false-positive detector.

Full configuration reference:

```text
docs/CONFIGURATION.md
```

## Supported Detection Areas

The detector package tree is organized by language, category, and tool:

```text
internal/detectors/languages/
  golang/
    ai/openai
    cache/redis
    cli/cobra
    di/wire
    http/echo
    http/fiber
    http/gin
    http/nethttp
    httpclient/nethttp
    httpclient/resty
    rpc/grpc
  java/
    db/jdbc
    db/jpa
    http/spring
    httpclient/feign
    httpclient/retrofit
    queue/kafka
    queue/sqs
  python/
    aws/sam
    cache/redis
    cli/argparse
    http/django
    http/fastapi
    http/flask
  javascript/
    http/express
  typescript/
    http/nestjs
  php/
    http/laravel
  ruby/
    http/rails
  dotnet/
    http/aspnet
```

When adding support for a new framework, add it under this tree and register it
in `internal/detectors/register/register.go`.

## How DiffMind Fits With DiffMind

Normally engineers do not run DiffMind one repository at a time. DiffMind starts
DiffMind for selected repositories, tracks status, and builds the multi-service
graph from the generated DiffMind protocol files.

Use direct DiffMind CLI runs for:

- debugging extraction on one service,
- testing a new detector,
- validating a `diffmind-configuration.yaml`,
- CI checks for a single repository.

## Troubleshooting

`runtime.pipeline=llm is no longer supported`

: Remove old LLM/OpenCode settings from central config. DiffMind is
  deterministic-only.

`unknown detector "go.echo"`

: The configuration references an old detector ID. Remove it or use a current
  detector ID from the registry. Normal configs should not enable detectors
  manually.

No endpoints/dependencies found

: Check that the repository contains supported source files and that
  `paths.exclude` is not excluding the code. Run with `--verbose`.

Missing service names or unresolved targets

: Add service aliases, HTTP target mappings, or resource patterns to
  `diffmind-configuration.yaml`.

Dirty working tree warning

: DiffMind records whether the analyzed repository was dirty. Generated
  `.diffmind` and `.diffmind` outputs are ignored by source filtering, but real
  source/config changes still make the run non-reproducible.
