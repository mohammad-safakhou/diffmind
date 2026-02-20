# M10 Plan: Multi-Codebase Service Graph + Web Client (Go-first)

## Summary

Add a new **Graph capability** that ingests extraction outputs from one or many repositories, resolves services/resources, and produces a navigable system graph with evidence-backed edges.

This plan is aligned with locked decisions:

- Scope: **single-repo + multi-repo in v1**
- Client: **Web UI + API**
- Frontend: **React + TypeScript**
- Identity: **service registry manifest + heuristics fallback**
- Edge policy: **verified deterministic edges by default**, inferred edges toggleable
- Storage: **both artifact files and Postgres**

## 1) Product Outcomes and Success Criteria

### Outcomes

1. You can build a graph from one repo or many repos.
2. Graph shows:
   - service-to-service API calls
   - service publish/consume queue relationships
   - service DB read/write relationships
3. Every default-visible edge is evidence-backed and inspectable.
4. A web client renders graph, filters, and evidence drilldown.

### Success Criteria

1. Given 4-service fixture (A/B/C/D), graph contains:
   - A -> B (API edges with endpoint details)
   - A -> Queue and Queue -> B
   - D -> DB and C -> DB
2. `graph build` finishes and writes graph artifact + DB rows.
3. `serve` exposes graph endpoints consumed by UI.
4. UI can filter edge types, confidence, repo, and inferred toggle.

## 2) Architecture and Modules (modular, independently runnable)

Add new modules:

1. `internal/graphschema`
   - Graph types/contracts (nodes, edges, evidence refs, graph metadata).
2. `internal/graphbuild`
   - Build graph from inputs (bundles + analyzer bundles + service registry).
   - Single repo and multi-repo modes.
3. `internal/graphresolve`
   - Deterministic resolvers:
     - service identity resolution
     - API edge resolution
     - queue publish/consume resolution
     - DB read/write resolution
     - cross-repo matching
4. `internal/graphstore`
   - File artifact writer/reader.
   - Postgres persistence adapter.
5. `internal/graphapi`
   - HTTP handlers for graph retrieval/evidence drilldown.
6. `ui/graph-client`
   - React/TS app using graph endpoints.
   - Built as static assets and embedded into Go server binary.
7. CLI wiring
   - New commands:
     - `extractor graph build ...`
     - `extractor graph query ...` (optional convenience)
     - `extractor graph serve ...` (or extend existing `serve`)

## 3) New Inputs and Contracts (decision-complete)

### 3.1 Service Registry Manifest (required in multi-repo, optional in single-repo)

File: `graph/services.yaml` (or JSON equivalent)

Per service:

- `id` (stable key)
- `name`
- `repo_path` or `repo_id`
- `bundle_path` (canonical bundle)
- `analyzer_bundle_path` (facts + evidence for drilldown)
- `base_urls` (domains/hosts this service owns)
- `queue_topics` (publish/consume known names)
- `dbs` (logical db names / schemas / connection aliases)
- `environment` (optional: prod/stage/dev)

Default behavior:

- Use registry first.
- Fill missing identity/resource mappings via deterministic heuristics.

### 3.2 Graph Artifact

File output: `<out>/graph/<graph_id>/graph.json`

Top-level:

- `graph_id`
- `generated_at`
- `mode` (`single`/`multi`)
- `nodes[]`
- `edges[]`
- `stats`

Node fields:

- `id`, `type` (`service|endpoint|queue|database|topic|table`)
- `label`
- `service_id` (where relevant)
- `attributes`
- `confidence`
- `inferred` bool

Edge fields:

- `id`, `type`
  - `service_calls_service`
  - `service_calls_endpoint`
  - `service_publishes_queue`
  - `queue_delivers_to_service`
  - `service_reads_db`
  - `service_writes_db`
- `source_id`, `target_id`
- `attributes` (method/path/topic/db/query hints)
- `confidence`
- `inferred` bool
- `evidence_refs[]` (deterministic source references)

Evidence ref fields:

- `snapshot_id`
- `file_path`
- `start_line`, `start_col`, `end_line`, `end_col`
- `fact_id`/`evidence_id` (if available)

## 4) API Additions

Extend `serve` with graph routes:

1. `GET /graphs`
   - List available graph ids + summaries.
2. `GET /graphs/{graph_id}`
   - Full graph payload.
   - Query params:
     - `include_inferred=false` default
     - `edge_types=...`
     - `service=...`
     - `repo=...`
3. `GET /graphs/{graph_id}/evidence/{edge_id}`
   - Returns normalized evidence refs + source linkage metadata.
4. `POST /graphs/build` (optional in v1 if CLI-first)
   - Build graph from manifest payload or manifest path.

Keep existing endpoints unchanged.

## 5) Persistence (DB + file)

### New tables (migration `000003_graph.sql`)

1. `graph_runs`
   - build metadata/status/timing.
2. `graphs`
   - graph identity + mode + generated_at + artifact_path.
3. `graph_nodes`
   - node rows keyed by `(graph_id, node_id)` with JSONB attributes.
4. `graph_edges`
   - edge rows keyed by `(graph_id, edge_id)` with JSONB attributes and inferred/confidence.
5. `graph_edge_evidence`
   - normalized evidence refs per edge.

Indexes:

- graph_id
- node/edge type
- source/target
- confidence
- inferred

## 6) Resolver Logic (deterministic first)

### 6.1 Service identity

1. Registry mapping first (`repo_path -> service_id`).
2. Fallback heuristic:
   - runtime units in `cmd/`, `main.go`, docker entrypoints.
   - module/repo naming.
   - endpoint clustering by runtime file paths.

### 6.2 API edges

1. From `ExternalCall` entities:
   - parse host/target/method.
   - map target host/path to owning service via registry `base_urls`.
2. If local runtime + endpoint path match inside same service, classify internal edge.
3. Build:
   - `service_calls_service`
   - optional `service_calls_endpoint` child edges.

### 6.3 Queue edges

Add deterministic detectors in analyzers for common libs/patterns (Go-first):

- Kafka (`sarama`, `segmentio/kafka-go`)
- RabbitMQ (`amqp`)
- SQS/SNS (AWS SDK)

Emit facts:

- `QueuePublish`, `QueueConsume` (or normalize via `ExternalCall` + attrs)

Then resolver forms:

- `service_publishes_queue`
- `queue_delivers_to_service`

### 6.4 DB edges

Add deterministic detectors:

- SQL open/init (`database/sql`, pgx, gorm config)
- migration ownership (`scripts/migrations`, flyway/liquibase patterns)

Emit facts:

- `DatabaseRead`, `DatabaseWrite` (or normalized attrs)

Resolver maps DB aliases/DSN hosts/schema names to registry `dbs` and forms:

- `service_reads_db`
- `service_writes_db`

### 6.5 Inferred edges

- Generated separately with lower confidence.
- Hidden by default; UI toggle to show.
- Must still carry evidence refs and `inferred=true`.

## 7) Client (React + TS, embedded)

### Stack

- Go-embedded static web client (HTML/CSS/JS)
- SVG-based graph rendering with interactive controls (pan/zoom/layout/focus)
- Assets embedded into Go binary (`embed.FS`) and served by `graphapi`.

### Views and interactions

1. Graph canvas
   - node type styling
   - edge type styling
   - direction arrows
   - layout selector (force/cose + hierarchical)
2. Filters
   - inferred toggle (off by default)
   - edge type
   - service/repo
   - confidence threshold
   - search by service/endpoint/topic/db
3. Detail panel
   - node/edge attributes
   - evidence list with file path + spans
   - quick links to related nodes
4. Modes
   - single-repo graph
   - multi-repo system graph

## 8) CLI/UX Additions

1. `extractor graph build --manifest graph/services.yaml --out .diffmind`
2. `extractor graph build --bundle ... --analyzer-bundle ... --service-id ... --out ...` (single-repo mode)
3. `extractor graph serve --addr :8080 --graph .diffmind/graph/<id>/graph.json` (or reuse existing `serve` config)
4. Make targets:
   - `graph-build`
   - `graph-serve`
   - `graph-ui-build`

## 9) Testing and Acceptance

### Unit tests

1. Service resolver (registry + fallback).
2. API edge mapper host/path -> service.
3. Queue publish/consume pairing.
4. DB read/write ownership resolution.
5. inferred toggle filtering.

### Integration tests

1. Synthetic A/B/C/D fixture corpus:
   - A calls 2 B APIs
   - A publishes queue, B consumes
   - D writes DB, C reads DB
2. Verify graph node/edge counts and exact edge types.
3. Verify evidence refs exist and map to valid spans.

### API tests

1. `/graphs` list response.
2. `/graphs/{id}` filtering semantics.
3. `/graphs/{id}/evidence/{edge_id}` correctness.

### UI tests

1. Graph renders expected nodes/edges from fixture graph JSON.
2. Filtering toggles change edge visibility correctly.
3. Evidence panel opens with expected refs.

### Performance checks

1. Graph build for medium corpus < target threshold (define baseline).
2. UI interaction remains responsive for N nodes/edges baseline.

## 10) Rollout Plan

1. Phase 1: contracts + file graph build for single repo.
2. Phase 2: registry + multi-repo merge + API endpoints.
3. Phase 3: queue/db resolvers + DB persistence.
4. Phase 4: React client + embedded serving.
5. Phase 5: A/B/C/D acceptance fixtures + CI gating.

CI additions:

- graph build + graph API tests
- fixture acceptance graph assertions
- UI build + smoke tests

## 11) Backward Compatibility

1. Existing extraction pipeline and bundle schema remain valid.
2. Graph feature is additive.
3. Existing `query/diff/serve` endpoints remain unchanged.
4. New analyzers/facts are additive; no breaking change to current consumers.

## 12) Assumptions and Defaults (locked)

1. Graph supports both single- and multi-repo in v1.
2. Web client + API is required in v1.
3. React+TS client is accepted despite Go-first core.
4. Service registry manifest is primary source; heuristics fill gaps.
5. Verified deterministic edges are default-visible.
6. Inferred edges are stored but hidden by default.
7. Graph data is persisted to both files and Postgres in v1.
8. UI is packaged as separate `ui` module and embedded into Go server binary.
