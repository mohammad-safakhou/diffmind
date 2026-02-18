# DiffMind Future Architecture Plan: Tool-Augmented, Query-First, Accuracy-First

## Summary
This plan moves DiffMind from primarily built-in detector logic to a hybrid extraction platform with a stable semantic core and pluggable adapters powered by language-native semantics, Semgrep, and CodeQL.
The first delivery wave prioritizes `Exposure` and `Dependencies` quality (the biggest current gap), then expands language coverage and deep logic mapping, while keeping strict pre-publish verification for critical edges.

Current repo grounding this plan:
1. The extractor has a built-in SDK and detectors in `internal/analyzers/extractor_sdk.go` and `internal/analyzers/detectors.go`.
2. Graph building currently depends mostly on `Endpoint` and `ExternalCall` entities in `internal/graph/resolve.go`.
3. The roadmap, KPI gates, and quality runbooks already exist in `docs/roadmap_state_of_the_art.md`, `docs/roadmap_state_of_the_art_kpi_targets.md`, and `docs/m14_quality_runbook.md`.

## Decision Lock (Confirmed)
1. Tool strategy: Hybrid core + adapters.
2. Deployment mode: Fully self-hosted.
3. Verification policy: Strict pre-publish verify.
4. Priority wave: Exposure + Dependencies first.
5. Static engines: Compiler/LSP + Semgrep + CodeQL.
6. Runtime truth: Phase 2 after static baseline.
7. Language rollout: Java -> TS/JS -> Go -> Python.

## Architecture Blueprint
1. Keep a stable semantic IR in core DiffMind; external tools feed normalized findings, never product-facing raw tool output.
2. Introduce adapter layers for language semantics, rule engines, and dataflow engines; adapters emit the same canonical fact contract.
3. Add fusion and adjudication layer that merges signals, scores confidence, detects contradictions, and marks verification state.
4. Apply strict publish policy for `Exposure` and `Dependency` classes: only verified or policy-allowed disputed claims are visible by default.
5. Preserve full evidence lineage and provenance per node/edge to satisfy explainability and enterprise audit constraints.
6. Keep runtime telemetry ingestion as a later reconciliation plane, not a day-one blocker.

## Execution Roadmap (Decision-Complete)
1. **W1: Ontology v2 freeze and compatibility policy.**
Why: avoid schema drift while introducing richer semantics.
Build: define canonical classes and types for exposure/dependency/logic, plus required metadata (`confidence`, `verification_state`, `evidence_refs`, `provenance`, `environment`, `time_validity`).
Exit criteria: versioned ontology and migration spec published; compatibility mapping from current entity types approved.

2. **W2: Adapter runtime and contracts in analyzer plane.**
Why: external tools must plug in without rewriting orchestration.
Build: extend SDK contracts in `internal/analyzers/extractor_sdk.go` with adapter lifecycle (`probe`, `plan`, `run`, `normalize`), capability declaration, deterministic replay metadata, and adapter health diagnostics.
Exit criteria: orchestration can run built-in and external adapters in one deterministic plan.

3. **W3: Self-hosted toolchain packaging and offline execution.**
Why: enterprise requirement forbids hidden SaaS dependency.
Build: package tool runners, caches, and rule packs for offline use; add tool cache/version manifest and supply-chain attestations.
Exit criteria: full extraction works with no outbound network; reproducible tool versions pinned and auditable.

4. **W4: Java Exposure adapters (Wave 1A).**
Why: current observed gap is missing endpoints/schedulers/consumers in Java service graphs.
Build: Java semantic adapter from compiler/LSP model, Semgrep rules for Spring variants/custom wrappers, CodeQL query pack for framework-agnostic endpoint and scheduler flow capture.
Exit criteria: Java graphs produce high-recall endpoint/scheduler/consumer nodes with evidence and framework attribution.

5. **W5: Java Dependency adapters (Wave 1B).**
Why: dependency truth is the second critical half of architecture mapping.
Build: extract outbound HTTP client calls, queue publishes/consumes, DB read/write surfaces, and command invocations using combined semantic/rule/dataflow adapters.
Exit criteria: dependency edge quality meets threshold targets and links to owning logic entities.

6. **W6: Resolver v2 for sectioned graph semantics.**
Why: extraction output must map cleanly to `Exposure | Logic | Dependencies` without UI-only heuristics.
Build: update resolver in `internal/graph/resolve.go` to consume ontology v2 classes and create typed section-aware nodes/edges; include normalization for paths/topics/resource aliases.
Exit criteria: section assignment is deterministic, evidence-backed, and independent of front-end guesswork.

7. **W7: Strict verification gate for publishable graph.**
Why: strict pre-publish policy for critical classes.
Build: verification policies in `internal/verifier` that block or quarantine low-confidence/conflicting exposure/dependency claims, with explicit disputed-state handling.
Exit criteria: default graph APIs expose verified set; policy can include disputed with explicit flag.

8. **W8: TS/JS adapters (Wave 1C).**
Why: large enterprise service surfaces are often JS/TS frameworks and custom routers.
Build: TypeScript compiler/LSP adapter, Semgrep route/client packs, CodeQL dataflow for route-to-client-to-resource chains.
Exit criteria: TS/JS exposure/dependency extraction reaches agreed precision/recall gates and parity with Java baseline behavior.

9. **W9: Go adapters (Wave 1D).**
Why: strengthen existing Go support from mixed regex/AST to robust semantic extraction.
Build: `go/packages` + `go/types` + SSA adapter, Semgrep pack for router/client libs, CodeQL pack for critical flow confirmation.
Exit criteria: Go extraction quality exceeds current detector performance and reduces fallback dependency.

10. **W10: Python adapters (Wave 1E).**
Why: complete first-wave multi-language coverage aligned with roadmap.
Build: pyright/libcst/ast adapter, Semgrep packs for Flask/FastAPI/Django/celery patterns, CodeQL pack for API/client/dataflow critical paths.
Exit criteria: Python exposure/dependency outputs meet same contract and gating behavior as other languages.

11. **W11: Query/API/UI contract upgrade for section-first graph consumption.**
Why: enterprise products must query sections and semantics directly, not parse raw type names.
Build: extend APIs in `internal/httpapi/module.go` to filter by section, class, verification state, and provenance; update UI in `internal/httpapi/ui/app.js` to consume section metadata from API, not infer from labels.
Exit criteria: query and UI both operate on canonical sectioned graph contracts.

12. **W12: Quality harness, rollout gates, and runtime phase-2 preparation.**
Why: accuracy-first requires measurable pass/fail, not "looks better."
Build: benchmark corpora, framework/version matrix tests, drift detection, and release gates wired to existing M14 quality flow; define phase-2 runtime reconciliation interface without enabling it as publish blocker yet.
Exit criteria: quality gates enforce thresholds; runtime ingestion design is ready for next phase.

## Public API / Interface / Type Changes
1. Add ontology v2 schema files and validators in `internal/graphschema` and `internal/facts`.
2. Extend analyzer SDK contract in `internal/analyzers/extractor_sdk.go` with adapter-oriented interfaces and replay metadata fields.
3. Add adapter registry and execution planner under `internal/analyzers` with explicit ordering and merge precedence.
4. Introduce section/class/verifier-state fields in graph node/edge model in `internal/graphschema/types.go`.
5. Add strict publish policy controls to graph build and serve APIs in `internal/graph` and `internal/httpapi/module.go`.
6. Add query filters for `section`, `class`, `verification_state`, `adapter_id`, and `provenance_version`.
7. Add migration(s) under `migrations` to persist new graph metadata fields and verification states.

## Test Cases and Scenarios
1. Java fixture correctness: controllers, schedulers, and SQS/Kafka consumer patterns must create non-zero exposure nodes with exact evidence spans.
2. Java dependency correctness: outbound HTTP, queue publish, DB read/write edges must be present and linked to logic ownership.
3. Strict publish policy: low-confidence/disputed critical edges are hidden by default and only visible with explicit policy override.
4. Framework/version matrix: Spring, Express/Fastify/Nest, Go router variants, Flask/FastAPI/Django across selected versions must pass extractor regression suites.
5. Custom wrapper resilience: organization-specific wrappers around routing/clients/queues are captured through Semgrep/CodeQL packs without core code changes.
6. Deterministic replay: identical commit + adapter/rule versions produce semantically equivalent graph (`>=99.99%` parity target).
7. Explainability completeness: every returned exposure/dependency edge includes evidence refs and provenance chain.
8. Fallback behavior: when adapter/tool unavailable, fallback path emits `needs_review` state rather than silent omission.
9. Performance baseline: extraction latency and memory are tracked per adapter and compared against baseline thresholds.
10. Security checks: self-hosted execution and artifact retention satisfy tenant isolation and audit requirements.

## Rollout and Governance Plan
1. Roll out per language wave with canary repositories and mandatory quality gate pass before widening.
2. Version rule packs independently from core binaries; enforce compatibility manifest.
3. Maintain rollback path for adapter, rule-pack, and schema releases using existing versioning policy.
4. Require signed extraction quality reports before promoting graph outputs to product APIs.
5. Keep runtime telemetry ingestion disabled by default until phase-2 readiness gates pass.

## Assumptions and Defaults Chosen
1. DiffMind remains the canonical truth and query engine; external tools are signal providers only.
2. Fully self-hosted operation is mandatory for enterprise deployments.
3. Exposure and dependency claims are critical domains and are strict-gated before publish.
4. Initial external tool stack includes compiler/LSP adapters, Semgrep packs, and CodeQL packs.
5. Runtime telemetry is a second-phase reconciler and not required to publish initial graphs.
6. Language rollout order is fixed: Java first, then TS/JS, then Go, then Python.
7. Existing roadmap/KPI contracts remain authoritative, with this plan acting as implementation-level execution detail for that roadmap.
