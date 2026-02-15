# Getting Started: Extractor + Service Graph

This guide runs the full flow with clean state and verifies outputs at each stage.

## 1) Prerequisites

- Go installed
- Docker installed
- `tree-sitter` CLI installed
- Optional: `OPENAI_API_KEY` in `.env` (only needed for `--llm-augment`)

## 2) Boot dependencies

From repo root:

```bash
cp .env.example .env
make up
make migrate
```

Health checks:

```bash
docker ps --format 'table {{.Names}}\t{{.Status}}'
```

## 3) Start from clean local output

```bash
rm -rf .diffmind
```

If you also want to wipe persisted DB/object data:

```bash
make down
docker volume rm diffmind_postgres_data diffmind_minio_data || true
make up
make migrate
```

## 4) Run extractor pipeline for one repository

```bash
go run ./cmd/extractor run --source . --ref HEAD --out .diffmind --persist
```

Expected outputs:

- `.diffmind/run/report.json`
- `.diffmind/bundle/intelligence_bundle.json`
- `.diffmind/analyzers/bundle.json`

Quick check:

```bash
jq '.status,.stages | length' .diffmind/run/report.json
```

## 5) Build graph (single-repo mode)

```bash
go run ./cmd/extractor graph build \
  --mode single \
  --service-id local \
  --service-name local \
  --bundle .diffmind/bundle/intelligence_bundle.json \
  --analyzer-bundle .diffmind/analyzers/bundle.json \
  --out .diffmind
```

Find latest graph artifact:

```bash
ls -1 .diffmind/graph
```

Inspect summary:

```bash
jq '{graph_id,mode,stats}' .diffmind/graph/*/graph.json
```

## 6) Run API + client

```bash
go run ./cmd/extractor serve \
  --addr :8080 \
  --bundle .diffmind/bundle/intelligence_bundle.json \
  --graph-root .diffmind/graph
```

Then open:

- `http://localhost:8080/` (graph client)

API checks:

```bash
curl -s http://localhost:8080/health | jq
curl -s http://localhost:8080/graphs | jq
```

## 7) Compare graphs

Build a second graph (after code changes or different manifest), then:

```bash
curl -s -X POST http://localhost:8080/graphs/compare \
  -H 'content-type: application/json' \
  -d '{"from_graph_id":"<old>","to_graph_id":"<new>"}' | jq
```

List compare history:

```bash
curl -s 'http://localhost:8080/graphs/compare?limit=20' | jq
```

Delete one compare:

```bash
curl -s -X DELETE http://localhost:8080/graphs/compare/<compare_id> | jq
```

Prune and keep only latest 20:

```bash
curl -s -X DELETE 'http://localhost:8080/graphs/compare?keep_latest=20' | jq
```

## 8) Multi-codebase graph

Run extraction separately for each repo:

```bash
go run ./cmd/extractor run --source /path/repo-a --ref HEAD --out /tmp/diffmind/repo-a --persist
go run ./cmd/extractor run --source /path/repo-b --ref HEAD --out /tmp/diffmind/repo-b --persist
```

Option A: one command (discover + build done internally):

```bash
go run ./cmd/extractor graph build --mode multi --sources /tmp/diffmind --out .diffmind
```

Option B: explicit discover manifest first (useful if you want to inspect/edit it):

```bash
go run ./cmd/extractor graph discover \
  --sources /tmp/diffmind/repo-a,/tmp/diffmind/repo-b \
  --manifest-out graph/services.yaml
```

This scans extraction outputs, detects service IDs, repo paths (from run reports when available), queue/db usage hints, and inferred base URLs.

Build combined graph:

```bash
go run ./cmd/extractor graph build --mode multi --manifest graph/services.yaml --out .diffmind
```

Why outputs stay separated:

- service identity is keyed by `service_id` (plus repo metadata), so node/edge ownership is explicit even when graph is merged.

## 9) Useful troubleshooting

- No graphs in API list:
  - verify `--graph-root` points to directory containing `<graph_id>/graph.json`.
- Empty graph:
  - verify extraction produced `bundle` and `analyzer_bundle` with runtime + external call facts.
- Missing cross-service edge:
  - verify manifest `base_urls`/queue/db names align with observed call targets.
- DB has old data:
  - reset Docker volumes, rerun migrations, then rerun extraction.

## 10) Fast local verification

```bash
go test ./...
go test ./internal/graph -bench BenchmarkBuildGraphMediumFixture -benchmem -run '^$'
```
