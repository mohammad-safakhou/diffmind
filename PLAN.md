# DiffMind Repo Extractor Plan

## 1. Target Outcome

Build a repo understanding engine that can:

1. Snapshot any repo at a ref with reproducible file inventory and hashes.
2. Parse and classify repository content across code + configs.
3. Extract deterministic, evidence-linked facts (runtime units, endpoints, config keys, CI/IaC details).
4. Optionally augment extraction with LLM inference under strict evidence and provenance controls.
5. Persist all artifacts/facts in open-source storage and support snapshot diffs.

All persisted facts must be auditable back to exact source spans.

Mandatory engineering constraints:

1. Base implementation language is Go.
2. Non-Go components are allowed only when they provide clear technical advantage (for example parser bindings, specialized tooling, or optional ML/LLM support).
3. Code must prioritize readability and clean structure over cleverness.
4. Architecture must stay highly modular so each module can be run independently and replaced with minimal impact.

## 2. Proposed Tech Stack

1. Language: Go (service + analyzers + typing) as the primary implementation language.
2. DB: PostgreSQL (JSONB + GIN indexes).
3. Object storage: MinIO (S3-compatible).
4. Parsing:
   - Tree-sitter for source code.
   - Native parsers for YAML/JSON/TOML/INI/XML.
5. Job/runtime:
   - CLI first (`extractor run ...`), then worker mode.
6. Packaging:
   - Docker Compose for local infra (Postgres + MinIO).
7. Secondary languages:
   - Allowed only for isolated helper tooling when Go is impractical.
   - Must integrate through stable interfaces (CLI/API/artifact contracts), never as hidden core dependencies.

## 3. Engineering Principles

1. Modularity first:
   - Every major stage is its own module with explicit input/output contracts.
   - Each module must support standalone execution (`extractor <module> ...`) and orchestrated pipeline execution.
2. Clean interfaces:
   - Depend on interfaces, not concrete implementations.
   - Keep storage, parsing, and analyzer layers swappable.
3. Readability standards:
   - Small focused packages, clear naming, shallow call chains.
   - Prefer explicit logic over implicit magic.
4. Change safety:
   - Strong typing, schema validation, and versioned contracts.
   - Module-level tests plus integration tests.

## 4. High-Level Architecture

1. `cmd/extractor`
   - CLI entrypoint: `snapshot`, `scan`, `parse`, `analyze`, `bundle`, `run`.
   - Every command must run as standalone module mode.
2. `internal/snapshot`
   - Git fetch/checkout, file walk, hash inventory.
3. `internal/classifier`
   - Repo profile + capability scan with evidence.
4. `internal/parser`
   - Config parser + Tree-sitter pipeline + parser registry.
5. `internal/facts`
   - Fact/evidence schema, validation, canonical IDs.
6. `internal/analyzers`
   - Runtime units, endpoints, outbound calls, config keys, CI/IaC.
7. `internal/consolidation`
   - De-dup, normalization, Repo Intelligence Bundle generation.
8. `internal/llm`
   - Optional bounded augmentation using evidence packs.
9. `internal/store`
   - Postgres + MinIO repositories.
10. `internal/orchestrator`
   - Stage execution, versioning, retries, metrics.

## 5. Milestones

## M0 - Foundation (Week 1)

1. Bootstrap Go module + folder layout.
2. Add DB migrations framework.
3. Add local `docker-compose` with Postgres + MinIO.
4. Add structured logging + config loading.

Exit criteria:

1. `make up` starts infra.
2. `make test` runs.
3. Base CLI command executes.

## M1 - Snapshot + Artifact Store (Week 1-2)

1. Implement repo source acquisition (remote/local).
2. Implement inventory walker + hash + file classification.
3. Persist snapshot metadata and inventory.
4. Upload raw artifacts (per file and/or tarball).

Exit criteria:

1. Same `repo+ref` produces identical snapshot identity and file inventory.
2. Snapshot can be reconstructed from persisted artifacts.

## M2 - Classification + Capability Scan (Week 2)

1. Implement signature/rule engine.
2. Implement language/extension counters.
3. Emit repo labels + capability records + evidence.

Exit criteria:

1. Fast scan (seconds on medium repos).
2. Evidence paths present on every capability.

## M3 - Universal Parsing (Week 3-4)

1. Add structured config parser with canonical JSON trees.
2. Add Tree-sitter parser service and grammar registry.
3. Save parse artifacts keyed by `(snapshot_id, file_hash, parser_version)`.
4. Add fallback text artifact for unsupported files.

Exit criteria:

1. >=90% textual coverage on representative repos.
2. Node spans retrievable from artifacts.

## M4 - Fact/Evidence Contract (Week 4)

1. Define strongly typed models + DB tables.
2. Enforce validation: facts without evidence are rejected.
3. Add provenance fields (analyzer/version/deterministic/inferred).

Exit criteria:

1. Every persisted fact references valid evidence.
2. Fact can render exact source location.

## M5 - Deterministic Analyzers v1 (Week 5-6)

1. Runtime unit detector.
2. Inbound HTTP endpoint extractor.
3. Outbound HTTP client call extractor.
4. Config key extractor.
5. CI/IaC extractor.

Exit criteria:

1. Service repo run yields runnable units, endpoints, outbound calls, config keys, and CI/IaC facts when present.
2. All findings have evidence spans.

## M6 - Consolidation + Bundle (Week 6)

1. Canonical entity merge rules and stable ID generation.
2. Build `Repo Intelligence Bundle` JSON output.

Exit criteria:

1. No obvious duplicates.
2. Stable IDs across re-runs for same snapshot.

## M7 - LLM Augmentation (Optional, Week 7)

1. Evidence pack builder with strict token/file budgets.
2. JSON-schema constrained output decoding.
3. Mark inferred facts and preserve prompt/response traces.

Exit criteria:

1. Improved coverage on unsupported frameworks.
2. No persistence without evidence references.

## M8 - Query + Diff + APIs (Week 7-8)

1. Query layer for key views (`endpoints`, `config keys`, `runtime units`).
2. Snapshot-to-snapshot bundle diff.
3. CLI and/or HTTP API output endpoints.

Exit criteria:

1. Fast entity lookups by snapshot.
2. Human-readable and machine-readable diffs.

## M9 - Acceptance Harness + Hardening (Week 8)

1. Corpus runner and golden outputs.
2. Regression checks in CI.
3. Performance and failure-mode testing.

Exit criteria:

1. Corpus passes reliably.
2. Regressions are explainable by analyzer/parser version changes.

## 6. Detailed TODO Checklist

## Foundation

- [x] Initialize Go module and project layout (`cmd`, `internal`, `pkg` if needed).
- [x] Add Makefile targets: `up`, `down`, `test`, `lint`, `run`.
- [x] Create `docker-compose.yml` for Postgres + MinIO.
- [x] Add env config loader and `.env.example`.
- [x] Add migration tooling and first schema baseline.
- [x] Add CI pipeline for lint + unit tests.
- [x] Add module interface contracts for snapshot/classifier/parser/analyzer/consolidation/store.
- [x] Ensure each module has standalone CLI execution path.
- [x] Add Go style/lint rules that enforce readability and package boundaries.

## Snapshot + Storage

- [x] Define snapshot identity scheme.
- [x] Implement git source handler (remote clone + local path support).
- [x] Implement deterministic inventory walker.
- [x] Implement content hashing and file-type classification.
- [x] Persist `runs`, `snapshots`, `file_inventory`.
- [x] Store raw artifacts in MinIO.
- [x] Add reproducibility integration test.

## Classification

- [x] Build file-signature ruleset for languages/tools/frameworks.
- [x] Implement capability detector with evidence path collection.
- [x] Implement weighted multi-label scoring.
- [x] Add fixtures for at least 20 diverse repos.

## Parser Layer

- [x] Build parser registry abstraction (`Parse(file) -> artifact`).
- [x] Add config parsers (YAML/JSON/TOML/INI/XML).
- [x] Integrate Tree-sitter core and initial grammars (Go, JS/TS, Java, Python).
- [x] Add parser versioning strategy.
- [x] Store parse artifacts + span maps.
- [x] Add fallback text parser and warning telemetry.

## Fact Model

- [x] Define `Fact`, `Evidence`, `Provenance` Go structs.
- [x] Define JSON schema / validation pipeline.
- [x] Implement evidence hash generation.
- [x] Implement persistence tables and indexes for facts/evidence.
- [x] Enforce reject-on-missing-evidence rule.

## Deterministic Analyzers

- [x] Runtime unit analyzer (Go/Node/Java/Python + Dockerfile).
- [x] HTTP inbound analyzer (Spring, Express/Fastify, Gin/Echo/Fiber).
- [x] HTTP outbound analyzer (Go `net/http`, JS fetch/axios, Java clients).
- [x] Config key analyzer (`os.Getenv`, `process.env`, Spring `@Value`, etc.).
- [x] CI analyzer (GitHub Actions/GitLab/Jenkins basics).
- [x] IaC analyzer (Helm/K8s/Terraform basics).
- [x] Add analyzer versioning and changelog.

## Consolidation + Bundle

- [x] Define canonical entity schema (RuntimeUnit, Endpoint, ConfigKey, ExternalCall, PipelineStep, InfraResource).
- [x] Implement stable natural keys per entity type.
- [x] Implement merge rules and conflict resolution.
- [x] Generate Repo Intelligence Bundle JSON.
- [x] Add duplicate-detection tests.

## LLM Augmentation (Optional)

- [x] Build evidence pack selector with deterministic rules.
- [x] Define strict LLM output schema.
- [x] Add inferred fact tagging + confidence defaults.
- [x] Store prompt/response traces as artifacts.
- [x] Add kill-switch for LLM stage.

## Query + Diff

- [x] Implement read models for common queries.
- [x] Add `bundle diff` engine.
- [x] Expose CLI output formats (`json`, `table`).
- [x] Optional HTTP endpoints for UI integration.

## Quality + Operations

- [x] Create corpus runner for repo acceptance tests.
- [x] Add golden summary files and controlled updates.
- [x] Add benchmark suite (small/medium/large repo profiles).
- [x] Add observability: run metrics, error taxonomy, stage durations.
- [x] Add idempotency/retry semantics for failed stages.
- [x] Document rollback/version compatibility policy.

## 7. Definition of Done (Project-Level)

Project is considered done when all are true:

1. Reproducible snapshot + inventory for repo refs.
2. Deterministic extraction pipeline produces canonical bundles with evidence.
3. Facts are queryable and diffable across snapshots.
4. Test corpus consistently passes in CI.
5. Optional LLM mode is auditable, bounded, and never bypasses evidence contract.
6. Every core module can run independently via CLI and via orchestrated pipeline.
7. Core logic remains Go-based, with any non-Go usage isolated behind explicit interfaces.

## 8. Immediate Execution Order (First 10 Working Sessions)

1. Session 1: Bootstrap project skeleton + infra (`M0`).
2. Session 2: Snapshot ingestion and inventory hashing (`M1` part 1).
3. Session 3: Persist snapshots and artifact upload (`M1` part 2).
4. Session 4: Rule-based classification + capability evidence (`M2`).
5. Session 5: Config parsers + artifact storage (`M3` part 1).
6. Session 6: Tree-sitter integration + spans (`M3` part 2).
7. Session 7: Fact/evidence schema + validation gate (`M4`).
8. Session 8: Runtime + endpoint analyzers (`M5` part 1).
9. Session 9: Outbound/config/CI/IaC analyzers (`M5` part 2).
10. Session 10: Consolidation + bundle + baseline corpus test (`M6` + `M9` start).
