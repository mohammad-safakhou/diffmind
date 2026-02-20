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

Assess merge/link quality (M8-focused):

```bash
go run ./cmd/extractor graph assess \
  --index .diffmind/graph/index.json \
  --out .diffmind/graph/merge_quality_report.json \
  --fail-on-gate
```

Optional benchmark mode against expected links:

```bash
cat > .diffmind/graph/expected_links.json <<'JSON'
{
  "service_calls_service": [
    {
      "source_service_id": "checkout",
      "source_repo_path": "/repos/checkout",
      "target_service_id": "orders",
      "target_repo_path": "/repos/orders"
    }
  ],
  "canonical_service_aliases": [
    {
      "source_service_id": "orders-service",
      "source_repo_path": "/repos/orders",
      "canonical_key": "orders",
      "env_scope": "prod"
    }
  ]
}
JSON

go run ./cmd/extractor graph assess \
  --index .diffmind/graph/index.json \
  --expect-links .diffmind/graph/expected_links.json \
  --out .diffmind/graph/merge_quality_report.json \
  --fail-on-gate
```

Validation rules for `--expect-links`:
- `service_calls_service`: each item requires `source_service_id` and `target_service_id`.
- `canonical_service_aliases`: each item requires `source_service_id` and `canonical_key`.
- File must contain at least one non-empty benchmark section.

Inspect the assessment report:

```bash
jq '{graph_id,passed,metrics,gates,benchmark}' .diffmind/graph/merge_quality_report.json
```

Benchmark trend artifacts are also written to:
- `.diffmind/graph/history/index.json`
- `.diffmind/graph/history/<run_id>_<graph_id>.json`

Run merge-quality assessment via HTTP API:

```bash
curl -s -X POST http://localhost:8080/graphs/merge-quality \
  -H 'content-type: application/json' \
  -d '{
    "index_path": ".diffmind/graph/index.json",
    "expect_links_path": ".diffmind/graph/expected_links.json",
    "out_path": ".diffmind/graph/merge_quality_report.json",
    "fail_on_gate": true
  }' | jq
```

Graph-scoped variant (pins `graph_path` from `graph_id`):

```bash
curl -s -X POST http://localhost:8080/graphs/<graph_id>/merge-quality \
  -H 'content-type: application/json' \
  -d '{
    "expect_links_path": ".diffmind/graph/expected_links.json",
    "out_path": ".diffmind/graph/merge_quality_report.json",
    "fail_on_gate": true
  }' | jq
```

If `out_path` is omitted in the graph-scoped endpoint, the report is written to:
`.diffmind/graph/<graph_id>/merge_quality_report.json`.

On benchmark failures, inspect:
- `benchmark.service_calls_service.false_positive_samples`
- `benchmark.service_calls_service.false_negative_samples`
- `benchmark.canonical_service_aliases.false_positive_samples`
- `benchmark.canonical_service_aliases.false_negative_samples`

Run quality evaluation + gate with merge-linkage benchmark enforcement:

```bash
go run ./cmd/extractor quality evaluate \
  --corpus .diffmind/corpus/report.json \
  --golden corpus/golden/summary.json \
  --merge-quality .diffmind/graph/merge_quality_report.json \
  --graph-index .diffmind/graph/index.json \
  --merge-quality-expect-links .diffmind/graph/expected_links.json \
  --merge-quality-auto \
  --out .diffmind/quality/report.json

go run ./cmd/extractor quality gate \
  --report .diffmind/quality/report.json \
  --policy quality/policy.json \
  --out .diffmind/quality/gate_result.json
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
