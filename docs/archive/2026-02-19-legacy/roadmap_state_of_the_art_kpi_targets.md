# DiffMind Roadmap - KPI Targets And Quality Gates

## 1. Purpose

This document defines strict, measurable quality targets for `M0` through `M17` in `roadmap_state_of_the_art.md`.

Use this as the release gate contract for implementation agents.

## 2. Global Non-Negotiable Gates

These gates must hold for all production-ready milestones unless explicitly exempted:

1. Evidence traceability coverage: `100%` of graph nodes and edges must include at least one valid evidence reference.
2. Explainability coverage: `100%` of query responses must support explain mode with evidence, provenance, and confidence.
3. Deterministic replay parity: same repo commit + same extractor/verifier versions must produce semantically equivalent facts at `>= 99.99%`.
4. Schema validation pass rate: `100%` for all produced artifacts.
5. Security policy enforcement pass rate: `100%` for protected APIs and data paths.

## 3. Domain Accuracy Targets

These are minimum acceptance thresholds for enterprise readiness.

| Domain | Precision | Recall | F1 | Notes |
|---|---:|---:|---:|---|
| Endpoint extraction | 0.98 | 0.95 | 0.965 | Includes method + path + direction |
| Service-to-service call edges | 0.97 | 0.93 | 0.949 | Internal and external calls |
| Queue publish detection | 0.97 | 0.93 | 0.949 | Includes queue/topic identity |
| Queue consume detection | 0.97 | 0.93 | 0.949 | Includes consumer role and topic |
| DB read/write detection | 0.96 | 0.90 | 0.929 | Includes operation type |
| Config key usage mapping | 0.99 | 0.95 | 0.969 | Includes source and service linkage |
| CI/CD workflow extraction | 0.98 | 0.92 | 0.949 | Includes build/test/deploy steps |
| Dependency graph extraction | 0.99 | 0.97 | 0.980 | Internal and third-party dependencies |
| Cross-repo identity resolution | 0.98 | 0.95 | 0.965 | Service and resource canonicalization |

## 4. Confidence And Calibration Targets

1. Confidence calibration ECE (expected calibration error): `<= 0.04`.
2. High-confidence slice precision (confidence `>= 0.9`): `>= 0.99`.
3. Medium-confidence slice precision (confidence `0.7 - 0.9`): `>= 0.95`.
4. Low-confidence unresolved ratio after verifier pass: `<= 5%` of total fact volume.
5. Contradiction detection recall on seeded conflict suite: `>= 0.97`.

## 5. Query Layer Targets

1. Question catalog coverage via query API: `>= 95%` by `M11`, `100%` by `M17`.
2. Explainable response coverage: `100%`.
3. Query correctness on gold QA corpus: exact match or semantically equivalent at `>= 0.95`.
4. Filter correctness (confidence, provenance, environment, time filters): `>= 0.995`.
5. Query planner regression tolerance: no correctness regressions allowed, performance regressions must stay within declared budgets.

## 6. Milestone KPI Gates

## M0

1. Question catalog completeness: `100%` of intended product categories mapped.
2. KPI catalog completeness: `100%` of critical domains have precision/recall targets.
3. Acceptance rubric completeness: `100%` for milestone-level pass/fail criteria.

## M1

1. Schema conformance: `100%` artifact validation in CI.
2. Ontology invariant test pass rate: `100%`.
3. Backward compatibility test pass rate: `100%` for non-breaking version increments.

## M2

1. Traceability coverage: `100%`.
2. Provenance chain integrity: `100%` for sampled and automated checks.
3. Reproducibility fingerprint presence: `100%`.

## M3

1. Extractor SDK adoption: `>= 90%` of extractors migrated, then `100%` before closing milestone.
2. Extractor unit test coverage: `>= 90%` line coverage, `>= 95%` branch coverage for critical paths.
3. Extraction regression detection sensitivity: `>= 0.98` on seeded failure suite.

## M4

1. Language-semantic extraction enabled for priority languages with thresholds from Section 3.
2. Regex-only fallback volume reduced to `<= 15%` of critical domain facts.
3. Framework-variant robustness: `>= 0.95` pass rate on framework fixture matrix.

## M5

1. Runtime/build/deploy graph linkage correctness: `>= 0.95`.
2. CI/CD step extraction F1: `>= 0.949`.
3. Artifact lineage correctness: `>= 0.95`.

## M6

1. Config mapping precision/recall meets Section 3 thresholds.
2. Secret-reference classifier false negative rate: `<= 1%` on benchmark corpus.
3. Environment scope attachment correctness: `>= 0.97`.

## M7

1. Dependency graph precision/recall meets Section 3 thresholds.
2. Ownership attachment correctness: `>= 0.95`.
3. Dependency drift detection precision: `>= 0.95`.

## M8

1. Cross-repo identity resolution precision/recall meets Section 3 thresholds.
2. Merge conflict false positive rate: `<= 2%`.
3. Provenance preservation after merge: `100%`.

## M9

1. Confidence calibration meets Section 4.
2. Contradiction detection recall: `>= 0.97`.
3. Undeclared unresolved contradictions in final graph: `0`.

## M10

1. Verifier precision on promoted facts: `>= 0.97`.
2. Low-confidence unresolved ratio: `<= 5%`.
3. Verifier-evidence compliance: `100%` claims linked to valid evidence IDs.

## M11

1. Query catalog coverage: `>= 95%`.
2. Explain mode coverage: `100%`.
3. Query correctness on gold corpus: `>= 0.95`.

## M12

1. Incremental recompute correctness parity with full recompute: `>= 0.995`.
2. Temporal query correctness: `>= 0.97`.
3. Graph diff correctness: `>= 0.97`.

## M13

1. Authorization correctness: `100%` policy test pass.
2. Audit log completeness: `100%` for mutating and sensitive operations.
3. Redaction correctness on sensitive corpus: `>= 0.99`.

## M14

1. Benchmark coverage: `100%` of critical domains and priority languages.
2. Quality gate enforcement coverage: `100%` in CI/CD.
3. Regression escape rate to production: `0` for severity-1 quality regressions.

## M15

1. Product features backed by query APIs: `100%`.
2. Product-level explainability availability: `100%`.
3. Product answer correctness against scenario corpus: `>= 0.95`.

## M16

1. SLO adherence for correctness-sensitive APIs: `>= 99.9%`.
2. Data integrity incident rate: `0` critical integrity incidents.
3. Rollback reliability for schema/extractor releases: `100%` tested success.

## M17

1. All milestone gates from `M0-M16` satisfied simultaneously.
2. Question catalog coverage: `100%`.
3. Explainability and traceability: `100%`.
4. Final quality attestation signed by engineering, platform, and security owners.

## 7. Release Decision Policy

1. Any failure in Global Non-Negotiable Gates blocks release.
2. Any domain metric below threshold blocks milestone closure for that domain.
3. Temporary waivers require explicit risk record and auto-expiry date.
4. Repeated metric regressions trigger extractor or resolver rollback, not threshold reduction.

## 8. KPI Reporting Contract

Each milestone completion report must include:

1. Metric values vs thresholds.
2. Confidence interval or sample size details.
3. Benchmark corpus version and coverage.
4. Regression deltas versus previous milestone.
5. Open risks and unresolved disputes.
