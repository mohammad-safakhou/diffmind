# DiffMind Consolidated Master Plan (Single Source Of Truth)

Last updated: 2026-02-18

## 1) Purpose
This document consolidates all planning and runbook documents under `docs/` into one executable program plan.
It is the canonical plan for milestone order, validation gates, and next implementation priorities.

Primary correction from recent sessions:
1. LSP-based extraction was planned but is not fully implemented.
2. LSP delivery is now moved to the immediate active milestone set under M4.

## 2) Aggregated Source Documents

### Program roadmap and quality governance
1. `docs/roadmap_state_of_the_art.md`
2. `docs/roadmap_state_of_the_art_dependency_map.md`
3. `docs/roadmap_state_of_the_art_kpi_targets.md`

### Architecture and extraction strategy
1. `docs/tool_augmented_architecture_plan.md`
2. `docs/ontology_v2_schema.md`
3. `docs/analyzer_adapter_runtime.md`
4. `docs/analyzer_offline_toolchain_policy.md`
5. `docs/versioning_policy.md`

### Graph and product delivery plans
1. `docs/M10_service_graph_plan.md`
2. `docs/getting_started_graph.md`
3. `docs/graph_performance_baseline.md`
4. `docs/m15_product_api_contracts.md`
5. `docs/m15_query_templates.json`
6. `docs/m17_question_catalog.json`

### Security, quality, operations, closeout
1. `docs/m13_security_architecture.md`
2. `docs/m14_quality_runbook.md`
3. `docs/w12_quality_harness_notes.md`
4. `docs/m16_operations_runbook.md`
5. `docs/m16_rollout_policy.json`
6. `docs/runtime_reconciliation_runbook.md`
7. `docs/m17_completion_runbook.md`

### Recent focused plans and investigations
1. `docs/enterprise_accuracy_ux_plan.md`
2. `docs/investigation_spikes_outcome.md`

## 3) Current State Snapshot (Reality Check)
Based on repository state and latest local artifacts:

1. Adapter runtime foundation exists, but only built-in adapter is currently available (`builtin`).
2. External language-tool adapters (LSP/compiler, Semgrep, CodeQL) are planned, not fully delivered.
3. Investigation spikes confirmed extraction quality gaps in Java outbound calls, queue resolution, and profile-aware config resolution.
4. Graph readability improved, but architecture signal is still mixed with context noise in dense cases.
5. Final closeout artifacts show incomplete closure for quality/ops/final-gate milestones (`M14`, `M16`, `M17` not fully passing).

Implication:
1. We should treat M4 as still open and move LSP implementation to immediate priority before claiming state-of-the-art closure.

## 4) Canonical Milestone Model
The canonical numbering remains `M0-M17` from `roadmap_state_of_the_art.md`.
Wave milestones (`W1-W12`) and focused recovery milestones map into this model.

### M0-M3 (Foundation) - Stabilized
1. M0 Program charter and question catalog: present.
2. M1 Ontology/schema baseline: present (v2 doc + section/verification taxonomy).
3. M2 Evidence/provenance backbone: present in baseline contracts.
4. M3 Extraction framework v2 + adapter runtime/offline policy: present (initially with builtin adapter only).

### M4 (Semantic Intelligence) - Active, Not Closed
M4 now explicitly includes LSP rollout as required, not optional.

M4.1 LSP Adapter Infrastructure (Immediate)
1. Add real external adapter registration and lifecycle (not only builtin).
2. Add deterministic tool execution contracts for LSP outputs.
3. Persist adapter-level provenance in emitted facts and graph nodes/edges.
Validation:
1. `--adapters` must run at least one non-builtin adapter successfully.
2. Replay parity test must pass for identical commit and tool versions.

M4.2 Java Accuracy Recovery (Immediate)
1. Implement Java typed outbound extraction (replace broad regex over-matching).
2. Implement Feign extraction with method/path/url resolution.
3. Add queue type guards and property-backed queue/topic resolution.
4. Exclude `src/test/**` from default enterprise graph mode.
Validation:
1. No known false HTTP targets from spike report.
2. Feign clients become resolved dependency edges with evidence.
3. Queue `unknown-*` reduced to unresolved-only true unknowns.

M4.3 Profile-Aware Config Resolution (Immediate)
1. Parse `application.yml`, `application-<profile>.yml`, and properties.
2. Merge per Spring precedence for `local`, `stage`, `prod`.
3. Resolve `${ENV:default}` placeholders with explicit unresolved markers.
Validation:
1. Profile-specific resolved config artifacts are produced.
2. Code property references link to profile-specific resolved values.

M4.4 TS/JS, Go, Python LSP Waves
1. TS/JS compiler/LSP adapter.
2. Go semantic + type/SSA adapter.
3. Python semantic/LSP adapter.
Validation:
1. Domain precision/recall meets M4 KPI targets per language fixtures.

### M5-M8 (Operational + Company Graph) - Continue After M4 Gates
1. M5 runtime/build/deploy/CI-CD intelligence.
2. M6 config and operational surface completeness.
3. M7 dependency/internal topology completeness.
4. M8 cross-repo/company graph resolution and canonical identity.
Validation:
1. E2E graph correctness against target service contracts plus multi-repo fixtures.

### M9-M12 (Verification, Query, Temporal) - Enforce Query-First Value
1. M9 confidence/conflict/adjudication as first-class graph state.
2. M10 agentic verification for low-confidence/disputed claims.
3. M11 query and explain APIs as primary product interface.
4. M12 temporal graph and incremental update correctness.
Validation:
1. Question-catalog coverage and explainability gates.
2. Incremental parity and diff correctness gates.

### M13-M17 (Enterprise Hardening and Final Gate)
1. M13 security/compliance enforcement and audit.
2. M14 quality harness as release blocker (not informational only).
3. M15 product contracts on query APIs only.
4. M16 reliability/SLO/rollback drills.
5. M17 final attestation and sign-off.
Validation:
1. All non-negotiable global gates from KPI contract must pass simultaneously.

## 5) Mapping: Existing Plans To Canonical Milestones
1. `W1` -> M1 (ontology v2 freeze).
2. `W2` and `W3` -> M3 (adapter runtime + offline policy).
3. `W4`, `W5`, `W8`, `W9`, `W10` -> M4 (language semantic/LSP/tool extraction waves).
4. `W6` -> M6-M8 interface (sectioned resolver semantics and graph mapping).
5. `W7` -> M9-M10 (verification gating and publish policy).
6. `W11` -> M11 (query/API/UI section-first contracts).
7. `W12` -> M14 (+ M16 preparation) quality gates and runtime reconciliation preparation.
8. `enterprise_accuracy_ux_plan` M1-M6 -> focused execution slice spanning M4, M6, M8, M11, M14.
9. `investigation_spikes_outcome` -> immediate implementation requirements feeding M4 and graph UX track.

## 6) Execution Order (Concentrated)
1. Close M4 completely (LSP infrastructure + Java/profile fixes + first language adapters + E2E quality gates).
2. Run M5/M6/M7 in parallel once M4 gates pass.
3. Merge into M8 cross-repo graph with deterministic identity.
4. Complete M9/M10 verification plane.
5. Complete M11 query-first serving and M12 temporal/incremental correctness.
6. Finalize M13/M14/M15/M16 and then run M17 final closeout.

Rule:
1. Do not mark downstream milestones closed using document presence only.
2. Milestone closure requires E2E gate evidence artifacts.

## 7) Mandatory Validation Framework (Per Milestone)
Each milestone must include all four validation modes:

1. Contract validation:
   expected-vs-actual architecture contract on target services.
2. Evidence validation:
   sampled fact/edge verification back to exact source spans.
3. Product validation:
   query/API/UI tasks must answer enterprise questions within defined thresholds.
4. Operational validation:
   deterministic replay, quality gate pass, SLO gate pass, rollback drill pass.

Minimum E2E service set:
1. `checkout-service` (primary reference).
2. At least two additional repositories with different stacks/frameworks.

## 8) Immediate Next Milestone (Active Now)
Current active milestone: **M4 complete closeout**.

Execution package for current cycle:
1. Implement non-builtin LSP adapter path and run it in analyzer pipeline.
2. Implement Java typed/Feign/queue extraction corrections.
3. Implement Spring profile config merge and property-link resolver.
4. Regenerate graph on `checkout-service` and publish mismatch report against expected contract.
5. Pass M4 acceptance metrics before moving to new feature milestones.

## 9) Deliverable Artifacts Required For Closure
1. Updated extraction report with adapter plans/runs and non-builtin adapter evidence.
2. Resolved config-by-profile artifact and linkage report.
3. Expected-vs-actual mismatch report for target service architecture surfaces.
4. Quality gate artifact showing M4 domain metrics pass thresholds.
5. Updated runbook references if commands/flags/contracts changed.

## 10) Governance Rules For Future Planning
1. Keep this file as the only execution source-of-truth.
2. Keep supporting docs as specification/runbook appendices, not competing plans.
3. Any new plan must explicitly map to `M0-M17` and update section 5 mapping.
4. No milestone can be called complete without E2E evidence artifacts linked in PR/report.
