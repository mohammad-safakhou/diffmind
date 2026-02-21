# Final Execution Plan: Exposure -> Dependency Intelligence

Updated: 2026-02-21

This is the single active plan. It replaces prior sequencing with a stricter compatibility-first execution model.

## 1) Product Target

Given a service path, DiffMind must produce a graph where:

1. The default view contains only three sections:
   - `Exposure` (left): endpoint, queue_consumer, scheduler, cli_input.
   - `Logic` (middle, initially hidden in default): functions/methods used for traceability.
   - `Dependencies` (right): external_http, dependency_operation, db_operation, queue_publish, external_system.
2. The default edge shown to operators is:
   - `exposure_reaches_dependency`.
3. Every default edge has evidence:
   - source file/line references, resolver trace id, confidence, provenance.
4. The UI is intentionally minimal:
   - graph canvas, node details panel, reset view, mode toggle (default/advanced), and search.
5. Querying is first-class:
   - operators can ask “which dependencies are reached by this exposure?” and get deterministic results.

## 2) Compatibility Policy (Hard Rule)

Only techniques that are 100% compatible with DiffMind requirements are allowed:

1. Must run self-hosted.
2. Must support deterministic replay.
3. Must produce evidence-backed claims.
4. Must degrade safely (mark uncertain) rather than hallucinate.
5. Must be language/version resilient without framework hardcoding as the only strategy.

## 3) Adopt/Reject Matrix From External Repos

### GitNexus

1. `ADOPT`: multi-phase indexing pipeline and deterministic graph identities.
2. `ADOPT`: precomputed structural artifacts for fast query-time traversal.
3. `ADAPT`: hybrid retrieval as a query assist layer, not truth generation.
4. `ADAPT`: impact/blast-radius style queries on top of verified graph edges.
5. `ADOPT`: interaction model for graph navigation (camera focus/reset, stable selection, strong highlight contrast).
6. `ADOPT`: filter/search UX patterns (type filters, edge filters, hop-depth focus, keyboard node search).
7. `ADOPT`: dense-graph performance behavior (edge-thinning during movement, restore when idle).
8. `REJECT FOR CORE`: hook-based search augmentation as a correctness dependency.
9. `REJECT FOR NOW`: storage-engine rewrite to KuzuDB before core accuracy is proven.

### deepwiki-open

1. `ADOPT`: configuration-driven provider/model routing (no hardcoded model logic).
2. `ADOPT`: provider abstraction for optional LLM adjudication.
3. `ADAPT`: long-running job status/progress patterns for extraction UX.
4. `REJECT FOR CORE`: embedding-only RAG/document-generation as architecture truth source.
5. `REJECT FOR CORE`: wiki-first flows before exposure->dependency precision gates pass.

## 4) Current Baseline (From Existing Implementation)

1. Root/leaf graph shape exists and basic UI simplification exists.
2. Multi-language adapter plumbing exists (`builtin`, `gopls`, `tsserver`, `pyright`, `jdtls`).
3. Resolver still has precision gaps in exposure->dependency mapping for some flows.
4. Validation is present but must be tightened around explicit ground-truth contracts per language and per framework family.

## 5) Target Architecture (Final Form)

1. `Ingestion`: snapshot + classification + parse (AST/tree-sitter + language-native parse artifacts).
2. `Semantic Adapters`: LSP/compiler adapters per language emit normalized symbols/calls/external references.
3. `Deterministic Extractors`: exposure/dependency extractors map semantic facts to ontology classes.
4. `Resolver`: builds executable exposure->dependency reachability with evidence paths.
5. `Verifier`: confidence policy and contradiction handling; ambiguous links move to `needs_review`.
6. `Graph Builder`: produces default root/leaf projection and advanced projection from same facts.
7. `Query Layer`: section-aware APIs and templates.
8. `UI`: minimal default graph and operator-friendly detail/inspection flow.
9. `Quality Gate`: repository contract comparison and release blocking.

## 6) Milestones (Mandatory, In Order)

### M1. Contract Freeze And Gold Dataset

Deliverables:
1. Freeze root/leaf ontology contract for default graph.
2. Create ground-truth contracts for at least one production-grade repo per language:
   - Java, Go, Python, TypeScript/JavaScript.
3. Add mismatch report format with machine-readable severity classes.

Validation:
1. Contract compare command blocks on critical mismatches.
2. Contracts include endpoint/consumer/scheduler/dependency operations with evidence anchors.

### M2. Language Semantic Core (No Hardcoded-Only Extraction)

Deliverables:
1. Make language semantic adapters mandatory when source contains that language.
2. Enforce capability gating:
   - missing required adapter -> fail closed (unless explicitly downgraded by policy flag).
3. Normalize semantic outputs to canonical facts:
   - `CodeSymbol`, `CodeCall`, `ExternalCall`, `Dependency`, `ConfigRef`.

Validation:
1. E2E run confirms adapter plan is deterministic and capability-gated.
2. Replay on same commit produces equivalent fact graph.

### M3. Exposure Extraction Precision/Recall Hardening

Deliverables:
1. Framework-agnostic exposure detection via semantic anchors first, rules second.
2. Support exposures:
   - HTTP routes, queue consumers/listeners, schedulers/cron/jobs, CLI inputs.
3. Attach input-shape metadata (path/query/body/event payload where available).

Validation:
1. Per-language exposure recall targets met against contracts.
2. No fallback regex-only claim is published without evidence level downgrade.

### M4. Dependency Operation Extraction Hardening

Deliverables:
1. Resolve concrete dependency operations:
   - outbound HTTP op, queue publish op, DB read/write op, external SDK op.
2. Resolve endpoint/topic/table/resource identity through config/property indirection.
3. Separate dependency identity from service-hub noise.

Validation:
1. Dependency precision and recall targets met per repository contract.
2. Placeholder/unknown dependency nodes below policy threshold.

### M5. Resolver V3 (Exposure -> Dependency Truth Engine)

Deliverables:
1. Deterministic call-path resolver from exposure entrypoint to dependency terminal operations.
2. Emit `exposure_reaches_dependency` with path evidence bundle.
3. Prevent false fan-out by requiring executable path support, not mere co-location or class membership.

Validation:
1. Manual sampled edges must match code-level traces.
2. False-link rate stays below agreed gate on contract suites.

### M6. Default Product Surface Lock

Deliverables:
1. Default API/view returns root/leaf graph only.
2. Advanced API/view returns logic path + context overlays.
3. UI keeps only required controls:
   - graph, select/highlight, details, search, reset, mode switch.
4. Implement GitNexus-inspired navigation essentials in DiffMind UI:
   - keyboard node search and focus jump.
   - stable select/neighbor highlight model with strong dimming of non-neighbors.
   - node type and edge type filters.
   - hop-depth focus filter from selected node.
5. Implement dense-graph interaction performance controls:
   - edge-thinning while pan/zoom is active.
   - restore full edge rendering when interaction settles.
   - preserve selection state while navigating.

Validation:
1. Operator can answer exposure->dependency questions without enabling advanced mode.
2. UI interaction tests pass for zoom/pan/select/highlight stability.
3. Focus and filter behavior is deterministic:
   - selecting from search always centers/focuses the same node and keeps detail panel synced.
4. Dense graph usability gate:
   - no accidental deselection while dragging/panning.
   - no page jump/scroll hijack during graph wheel interactions.
   - highlight/dimming remains readable under heavy edge count.

### M7. LLM Adjudication Lane (Strictly Bounded)

Deliverables:
1. Add LLM adjudicator for unresolved ambiguous links only.
2. LLM output is non-authoritative until verifier accepts evidence-backed rationale.
3. Persist model id, prompt template id, rationale, and confidence deltas.

Validation:
1. Deterministic pipeline without LLM remains valid baseline.
2. LLM path increases recall without violating precision gate.

### M8. Logic Path Expansion (Secondary Layer)

Deliverables:
1. Add explicit logic graph:
   - `exposure_invokes_function`, `function_calls_function`, `function_calls_dependency`.
2. Keep this layer hidden by default and queryable on demand.
3. Support argument/context propagation metadata where recoverable.

Validation:
1. Critical paths from contracts can be rendered end-to-end in advanced mode.
2. Root/leaf default quality must not regress.

### M9. Cross-Service Resolution

Deliverables:
1. Resolve dependency endpoints/queues to target-service exposures.
2. Build service-to-service company graph on verified contracts.
3. Support environment-aware aliases (prod/staging/base-url variations).

Validation:
1. Multi-repo contract suite verifies service-to-service links.
2. Query returns exposure->dependency->service mappings with evidence.

### M10. Query-First Enterprise Surface

Deliverables:
1. Finalize stable query endpoints for:
   - root/leaf lookup
   - impacted exposures/dependencies
   - contradiction/review queue
   - cross-service traversals.
2. Add saved query templates for common enterprise questions.
3. Add change-impact and PR-review primitives powered by verified graph.

Validation:
1. Query answers are reproducible on repeated runs.
2. All templates are backed by contract-tested semantics.

## 7) Quality Gates (Global)

1. No milestone closes with unit tests only.
2. Every milestone requires:
   - E2E run on real services.
   - expected-vs-actual contract report.
   - sampled manual verification report.
3. Release is blocked if any critical gate fails:
   - exposure recall
   - dependency precision
   - edge precision/recall
   - evidence completeness.

## 8) Execution Order

1. Execute M1 -> M6 first (root/leaf production contract).
2. Execute M7 -> M8 second (intelligent ambiguity handling + logic depth).
3. Execute M9 -> M10 last (company graph and enterprise query surface).
4. No milestone skipping.
