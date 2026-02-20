# DiffMind State-of-the-Art Roadmap

## 1. Purpose

Build an enterprise-grade, high-accuracy software intelligence platform that:

1. Extracts codebase truth with evidence.
2. Resolves and verifies ambiguous findings.
3. Produces a query-first, explainable company graph.
4. Powers products like PR review, mapping, auto-docs, and impact analysis.

This roadmap is accuracy-first and detail-first. Time and cost are secondary constraints.

## 2. Success Definition

The platform is successful when it can answer critical engineering questions with:

1. High precision and recall.
2. Full evidence traceability.
3. Clear confidence and uncertainty.
4. Strong enterprise access control and auditability.
5. Stable behavior across large multi-repo organizations.

## 3. Product Principles

1. Query-first, not dump-first.
2. Evidence before inference.
3. Deterministic extraction first, agentic verification second.
4. Every graph claim is explainable.
5. Contradictions are stored and adjudicated, never silently hidden.
6. Multi-repo identity and temporal history are first-class features.

## 4. Target Architecture

### 4.1 Planes

1. Extraction Plane
   Deterministic parsers and extractors produce observed facts plus evidence.
2. Verification Plane
   Agentic verifier performs targeted deep checks for low-confidence or conflicting facts.
3. Knowledge Plane
   Resolver canonicalizes entities and builds typed, versioned graph.
4. Serving Plane
   Query APIs expose graph, explainability, filtering, and change history.
5. Governance Plane
   Quality gates, provenance, RBAC, compliance, and audit.

### 4.2 Core Data Classes

1. Observed fact
   Machine-extracted, deterministic, evidence-backed.
2. Inferred fact
   Derived from observed facts and rules or model reasoning.
3. Verified fact
   Inferred or observed fact validated by additional checks.
4. Disputed fact
   Contradictory claims waiting adjudication.

### 4.3 Required Metadata On Every Fact/Edge

1. Confidence score.
2. Provenance (extractor/verifier ID and version).
3. Evidence references.
4. Commit/source identity.
5. Environment scope (dev/stage/prod when known).
6. Time range/version validity.

## 5. Milestones

---

## M0 - Program Charter And Question Catalog

### Objective
Define what the system must answer and how correctness is measured.

### Work Packages
1. Build a question catalog from enterprise use cases.
2. Define truth classes and quality definitions per class.
3. Define KPI baselines by domain (API, queue, DB, CI/CD, config, dependency).
4. Define acceptance criteria for state-of-the-art readiness.

### Deliverables
1. Product question catalog.
2. Quality charter.
3. KPI baseline document.

### Exit Criteria
1. All downstream milestones map to explicit question categories.
2. KPI targets are frozen and signed.

---

## M1 - Ontology And Schema Contracts

### Objective
Standardize graph model and contracts to avoid schema drift and ambiguous semantics.

### Work Packages
1. Define typed node schema.
2. Define typed edge schema.
3. Define required metadata and invariants.
4. Add schema versioning and compatibility policy.
5. Add strict validators in CI.

### Deliverables
1. Canonical ontology spec.
2. Versioned JSON schema files.
3. Validation suite with negative/edge tests.

### Exit Criteria
1. All artifacts validate against versioned schemas.
2. Breaking changes require explicit version bump and migration path.

---

## M2 - Evidence And Provenance Backbone

### Objective
Guarantee explainability and auditability of every graph assertion.

### Work Packages
1. Standardize evidence record format (path/span/hash/snippet policy).
2. Add immutable evidence storage and retrieval API.
3. Add provenance chain from query result to extraction run.
4. Add reproducibility fingerprint (repo, commit, extractor-set, config hash).

### Deliverables
1. Evidence store design.
2. Provenance API contract.
3. Explainability response format.

### Exit Criteria
1. Any node/edge can be traced to source evidence in one API call chain.
2. Re-running same commit with same extractor set is reproducible.

---

## M3 - Extraction Framework V2

### Objective
Move from ad-hoc detection toward a modular, benchmarkable extraction platform.

### Work Packages
1. Define extractor SDK contracts.
2. Split domain extractors (API/queue/DB/config/CI/runtime/dependency).
3. Add shared normalization utilities.
4. Add extractor test harness and benchmark harness.
5. Add extractor quality scorecards.

### Deliverables
1. Extractor SDK.
2. Extractor module registry.
3. Benchmark runner and reports.

### Exit Criteria
1. New extractors can be added without touching orchestration internals.
2. Quality regressions are caught automatically.

---

## M4 - Semantic Code Intelligence

### Objective
Increase precision by replacing regex-heavy extraction with language-semantic analysis.

### Work Packages
1. Go semantic extractor (routing, handlers, outbound calls, DB and queue use).
2. TS/JS semantic extractor (framework-aware endpoint and client call extraction).
3. Python semantic extractor.
4. Java semantic extractor.
5. Unified symbol/call model across languages.

### Deliverables
1. Language-specific semantic extractors.
2. Cross-language normalization layer.

### Exit Criteria
1. Priority language coverage reaches target precision and recall.
2. Endpoint and service-call extraction is robust across common frameworks.

---

## M5 - Runtime, Build, Deploy, And CI/CD Intelligence

### Objective
Model how systems run and ship, not only how code is written.

### Work Packages
1. Docker/Compose/K8s/Helm/Terraform extraction.
2. CI workflow parsing (GitHub/GitLab/Jenkins and extensible model).
3. Build artifact lineage extraction.
4. Deployment linkage to service/runtime entities.

### Deliverables
1. Runtime topology facts.
2. Build/deploy graph edges.
3. CI/CD knowledge model.

### Exit Criteria
1. System can answer "how this service builds, deploys, and runs" with evidence.

---

## M6 - Config And Operational Surface

### Objective
Build complete visibility into configuration, secrets references, and operational exposure.

### Work Packages
1. Extract config keys from code and config manifests.
2. Map config usage to service/runtime edges.
3. Detect secret-like references and classify sensitive surfaces.
4. Add environment-scoped config resolution.

### Deliverables
1. Config lineage model.
2. Config-to-runtime and config-to-service edges.
3. Sensitive surface metadata.

### Exit Criteria
1. Config questions are answerable by query with explain mode.

---

## M7 - Dependency And Internal Topology

### Objective
Capture internal and external dependency structure with high fidelity.

### Work Packages
1. Parse lockfiles/manifests and build dependency graph.
2. Distinguish internal packages from third-party dependencies.
3. Add ownership/team/domain metadata hooks.
4. Detect dependency drift and risk patterns.

### Deliverables
1. Dependency graph model.
2. Ownership and domain attachment model.

### Exit Criteria
1. Queries can resolve transitive dependency and ownership impact reliably.

---

## M8 - Cross-Repo Resolution And Company Graph

### Objective
Compose many repository graphs into one consistent enterprise graph.

### Work Packages
1. Service identity canonicalization across repos.
2. Alias normalization for hosts, queues, DB resources, and APIs.
3. Environment-aware graph merge rules.
4. Preserve repo-local provenance after merge.

### Deliverables
1. Cross-repo resolver.
2. Company graph builder.

### Exit Criteria
1. Multi-repo merge accuracy passes identity and linkage benchmarks.

---

## M9 - Confidence, Conflict, And Adjudication

### Objective
Treat uncertainty as product data instead of hiding it.

### Work Packages
1. Confidence scoring model and calibration.
2. Conflict detection across extractors/sources.
3. Fact status lifecycle (observed/inferred/verified/disputed).
4. Adjudication workflow API for automated and human review paths.

### Deliverables
1. Scoring engine.
2. Conflict store.
3. Adjudication APIs.

### Exit Criteria
1. Contradictory claims are represented explicitly and queryable.
2. Confidence calibration meets domain thresholds.

---

## M10 - Agentic Verification Plane (Codex-Inspired)

### Objective
Improve recall and precision in ambiguous areas using targeted exploration.

### Work Packages
1. Build verifier orchestrator for low-confidence queue.
2. Add strict verifier contract: no claim without evidence IDs.
3. Add two-pass verification (hypothesis and contradiction pass).
4. Merge verifier output as verified/disputed facts, never raw overwrite.

### Deliverables
1. Verification service.
2. Verification report format.
3. Merge rules for verified facts.

### Exit Criteria
1. Low-confidence bucket shrinks while maintaining precision.
2. Verifier results are reproducible and auditable.

---

## M11 - Query Language And Serving APIs

### Objective
Deliver the main product value: precise, explainable answers over the graph.

### Work Packages
1. Define query DSL and filter model.
2. Implement graph query planner and execution engine.
3. Add explain mode with evidence/provenance/confidence.
4. Add saved query templates for core enterprise use cases.
5. Add pagination and performance controls.

### Deliverables
1. Query DSL specification.
2. Query API endpoints.
3. Explain API contract.

### Exit Criteria
1. Priority question catalog is answerable via query API only.
2. Explain mode is available for all returned entities and relations.

---

## M12 - Temporal Graph And Incremental Updates

### Objective
Track evolution and support change-impact/time-travel analysis.

### Work Packages
1. Add temporal versioning model for nodes/edges/facts.
2. Add incremental recompute by changed files and impacted subgraph.
3. Add graph diff APIs across commits/releases.
4. Add freshness and staleness metadata.

### Deliverables
1. Temporal graph schema.
2. Incremental pipeline strategy.
3. Graph diff APIs.

### Exit Criteria
1. Queries support before/after analysis and release-time snapshots.

---

## M13 - Enterprise Security And Compliance

### Objective
Satisfy enterprise trust and governance requirements.

### Work Packages
1. Tenant isolation and RBAC/ABAC policy model.
2. Evidence redaction policy for secrets and sensitive fields.
3. Encryption and key management standards.
4. Full audit logs for extraction, verification, and query access.
5. Retention and compliance export workflows.

### Deliverables
1. Security architecture.
2. Policy enforcement engine.
3. Audit/compliance tooling.

### Exit Criteria
1. Security and compliance requirements pass internal audit readiness.

---

## M14 - Quality And Evaluation System

### Objective
Make accuracy measurable, repeatable, and enforceable.

### Work Packages
1. Build benchmark corpora by language and domain.
2. Track precision/recall/F1 and confidence calibration continuously.
3. Add adversarial and edge-case suites.
4. Add release gates by quality domain thresholds.
5. Add dashboards and regression triage runbooks.

### Deliverables
1. Evaluation harness.
2. Quality dashboards.
3. Release gating policy.

### Exit Criteria
1. Releases are blocked automatically on critical quality regressions.

---

## M15 - Product Layer On Top Of Query

### Objective
Ship high-value products powered by graph and query, not raw code duplication.

### Work Packages
1. PR reviewer powered by graph plus evidence plus diff.
2. Auto-documentation generator from query templates.
3. Service mapper and dependency impact tools.
4. Governance and risk reporting products.

### Deliverables
1. Product APIs and service contracts.
2. UX-level query templates and integration hooks.

### Exit Criteria
1. Each product function depends on queryable graph truth and explain mode.

---

## M16 - Reliability And Operations

### Objective
Run the system safely at enterprise scale.

### Work Packages
1. Define SLOs for ingestion, freshness, query latency, explain latency.
2. Add observability for extraction quality and query performance.
3. Add backup/restore and disaster recovery playbooks.
4. Add schema and extractor rollout strategy with safe rollback.

### Deliverables
1. Operational SLOs.
2. Runbooks and alerting.
3. Rollout and rollback policies.

### Exit Criteria
1. Platform meets reliability and operability standards under load.

---

## M17 - State-of-the-Art Completion Gate

### Objective
Formally certify that the platform has reached target enterprise quality.

### Work Packages
1. Run final benchmark and audit suite.
2. Validate complete explainability coverage.
3. Validate cross-repo and temporal query reliability.
4. Validate security/compliance readiness.

### Deliverables
1. Final readiness report.
2. State-of-the-art gate decision document.

### Exit Criteria
1. All mandatory KPIs and policy gates are satisfied.

## 6. Cross-Cutting Tracks (Run Across All Milestones)

1. Data model governance and schema evolution.
2. Developer experience for extractor and verifier plugin authors.
3. Documentation quality and internal onboarding.
4. Backward compatibility and migration discipline.
5. Tenant and policy enforcement in all APIs.

## 7. Implementation Sequence (Recommended)

1. M0 to M2 first: lock truth model, ontology, and evidence backbone.
2. M3 to M6 next: extraction precision and operational intelligence.
3. M7 to M10 next: company-graph resolution and verification plane.
4. M11 to M12 next: query-first core and temporal/incremental correctness.
5. M13 to M17 last: enterprise hardening, quality gates, product expansion, formal completion.

## 8. Immediate Next Iteration Plan (First Execution Slice)

1. Implement confidence and conflict engine skeleton.
2. Introduce extractor SDK contracts and split existing analyzers by domain.
3. Add semantic extraction path for first priority language.
4. Add low-confidence queue and verification stage stub.
5. Launch first explainable query API with provenance payload.

## 9. Definition Of Done For The Program

1. Priority enterprise questions are answerable via API without manual repo reading.
2. Each answer is evidence-backed with confidence and provenance.
3. Cross-repo graph quality meets target thresholds.
4. Temporal and diff queries are reliable.
5. Security, auditability, and compliance requirements are satisfied.
6. Product layer features are built on query contracts, not ad-hoc code scraping.
