# Enterprise Accuracy + UX Recovery Plan (Focused)

## Goal
Deliver enterprise-grade architecture extraction and graph UX for real services, with strict end-to-end validation on every milestone.

This plan is intentionally small and focused on current blocking gaps (accuracy, graph meaning, and usability), not a long feature wishlist.

## Current Gaps We Must Fix
1. Run integrity drift: extraction source path can differ from the repo the user thinks was scanned.
2. Dependency truth gap: external service calls are under-resolved (missing host/service/path quality).
3. Queue truth gap: queue names/topics are often `unknown-*` despite config values existing.
4. Exposure truth gap: scheduler/consumer exposure is underrepresented versus real code.
5. Graph meaning gap: ownership/meta edges dominate visualization and hide architecture flow.
6. UX gap: dense graph navigation and discovery are still slow for real-world service size.

## Milestones

## M1. Run Integrity And Reproducibility
### Scope
1. Make run metadata strict and visible: exact `source_root`, commit, profile, and extraction mode must be shown in CLI/API/UI.
2. Add a hard mismatch check so graph build fails if bundle provenance does not match selected source.

### Deliverables
1. Provenance mismatch guard in pipeline.
2. Graph/API metadata block with source identity.
3. Operator runbook update for reproducible runs.

### Validation (E2E)
1. Run extraction against `/checkout-service` and verify `source_root` in `.diffmind/analyzers/report.json` matches exactly.
2. Attempt build with a bundle from a different repo and verify the build is rejected with explicit error.
3. Open UI and confirm source identity is visible before graph rendering.

### Exit Gate
1. No graph can be built from mismatched provenance.
2. Operator can prove which exact repo/commit produced a graph in less than 30 seconds.

## M2. Ground-Truth Contract For Target Service (Investigation Spike)
### Scope
1. Build a signed expectation contract for `checkout-service`:
endpoint set, scheduler set, queue publish/consume set, Feign/outbound dependency set, DB interaction set.
2. Mark uncertain areas explicitly (custom wrappers, dynamic URL assembly, profile-specific overrides).

### Deliverables
1. `expected_graph_contract.json` for this service.
2. Gap list: `known-unknowns` and required extractor strategies.

### Validation (E2E)
1. Generate contract from code + config review and review with one human pass.
2. Run extractor + graph build and compare actual vs contract.
3. Publish mismatch report with exact missing/extra claims and evidence links.

### Exit Gate
1. Contract coverage is complete for critical architecture surfaces.
2. Unknowns are bounded and tracked explicitly (no silent ambiguity).

## M3. Extraction Fixes For Critical Architecture Surfaces
### Scope
1. Fix Java outbound extraction (Feign and semantic HTTP client resolution to service/path).
2. Resolve queue names/topics from config/property references.
3. Model scheduler and queue-consumer exposures as first-class exposure nodes.
4. Exclude non-architectural noise from default extraction scope (tests, noop/mock clients) while keeping optional debug mode.

### Deliverables
1. Updated extractors and resolver mappings for external calls, queues, schedulers.
2. Noise policy for default enterprise extraction mode.

### Validation (E2E)
1. Run full pipeline on `checkout-service`.
2. Compare to M2 contract:
- endpoint recall >= 98%
- scheduler recall >= 95%
- queue name resolution >= 95%
- outbound dependency precision >= 90%
3. For 15 sampled critical edges, open evidence and verify file/line and semantic correctness manually.

### Exit Gate
1. Critical architecture surfaces pass thresholds.
2. Evidence-backed sampled review passes with no critical wrong edge.

## M4. Graph Semantics Hardening (Topology vs Ownership)
### Scope
1. Separate edge families into:
- topology-flow edges (architecture meaning)
- ownership/meta/compliance edges (context)
2. Make topology-flow default in visualization and query templates.
3. Keep ownership/context accessible as optional overlay, not mixed by default.

### Deliverables
1. Edge taxonomy + API filter defaults.
2. Updated graph projection rules to avoid hub-noise by default.

### Validation (E2E)
1. Build graph from same service and load UI with default topology mode.
2. Verify no single ownership hub dominates view by default.
3. Run query for both modes and confirm counts are stable and explainable:
- topology result
- topology + ownership overlay result

### Exit Gate
1. Default graph communicates architecture flow, not metadata clutter.
2. Users can intentionally turn on ownership overlays when needed.

## M5. UI/UX Investigation + Redesign For Large Service Graphs
### Scope
1. Define and implement a clear analysis workflow:
Expose -> Logic -> Dependencies with grouped lanes and progressive disclosure.
2. Improve large-graph usability:
search-first navigation, stable focus panel, deterministic highlight paths, fit/reset controls, node detail drawer with full names and evidence shortcuts.
3. Add a UX investigation track for dense-layout strategy (hierarchical, dagre/elk, hybrid lane layouts).

### Deliverables
1. Updated UX flow and layout strategy for dense enterprise graphs.
2. Interaction model and visual defaults for readability at scale.

### Validation (E2E)
1. Task-based usability checks on `checkout-service`:
- find all exposures of one type
- trace one endpoint to its dependencies
- identify all scheduler-triggered paths
- export focused subgraph
2. Success criteria:
- each task completed in <= 60 seconds by a fresh operator
- no ambiguous node labels in detail panel
- no interaction dead-ends

### Exit Gate
1. User can answer architecture questions quickly without manual graph cleanup.
2. Dense graph navigation is stable and predictable.

## M6. Release Gate: End-To-End Evaluation Harness
### Scope
1. Convert milestone validations into executable E2E gates (not just unit tests).
2. Add service-level golden evaluations and API/UI checks as release blockers.

### Deliverables
1. E2E command suite:
extract -> graph build -> serve -> query -> compare -> report.
2. Scorecard artifact per run:
accuracy, completeness, explainability, UX task pass-rate.

### Validation (E2E)
1. Run harness on:
- `checkout-service` (primary)
- at least 2 additional real repos with different stacks
2. Block release if any critical metric or task gate fails.

### Exit Gate
1. Every release has objective architecture-quality proof.
2. Regression is caught before users see it.

## Investigation Backlog (Only Real Unknowns)
1. Dynamic endpoint/client construction patterns not resolved by static extraction.
2. Profile-specific config merge behavior that changes queue/service targets by environment.
3. Best dense graph layout strategy under 1k+ nodes with readable interaction cost.

## Execution Order
1. M1 -> M2 -> M3 -> M4 -> M5 -> M6

## Definition Of Done (Program-Level)
1. For target service, graph claims match real architecture surfaces with evidence-backed confidence.
2. Default UI shows meaningful architecture flow without manual cleanup.
3. Each release passes strict E2E validation with explicit expected-vs-actual reporting.
