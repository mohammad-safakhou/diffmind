# Release readiness and agent-assisted improvement plan

Status: implementation checkpoint, 8 September 2026. Baseline: source revision `808c226`; the implementing revision is recorded in Git history. This plan supersedes earlier broad readiness claims, not the implementation history in ROADMAP.md.

## Implementation checkpoint

The locally implementable M0-M6 slices are now present and covered by automated gates:

- M0: sanitized regressions cover unresolved destinations, OpenAPI contracts, pack wildcard aliases, repository source accounting, gap lifecycle, and installed agent/company workflows. `make readiness-report` records the commit, dirty state, platform, corpus and pack digests, step durations and output digests.
- M1: explicit unresolved HTTP targets remain inspectable but cannot create graph edges or phantom external nodes; quality counters retain them. Saved analysis state is distinct from live repository freshness in query/MCP output, and Python/Node/vendor environment trees are excluded from source metrics.
- M2: wildcard extraction works across sequences and mappings, repeated alias/resource results merge deterministically, and pack lint/test plus the existing signed-lock, activation and rollback paths remain release-gated.
- M3: bounded local OpenAPI 3.0 parsing enriches exact method/path exposures without remote fetches. Request fields, evidence locations and compatibility changes are searchable through query, HTTP and MCP interfaces.
- M4: project-local improvement gaps have stable cross-run fingerprints, optimistic revisions, an audited lifecycle, role-checked HTTP/agent operations and a private-by-default agent playbook. Activation status records a review decision; pack/config mutation still uses the existing explicit management operations.
- M5: doctor identifies the running executable and duplicate PATH candidates, tool discovery documents 13 read tools and 18 local management tools, and large graphs initially focus the largest team instead of rendering the entire organization at once.
- M6: the local candidate harness, race detector, vet, pack tests, both UI suites/builds, distribution smoke tests, filesystem/SQLite company acceptance, agent acceptance, dependency audits and vulnerability scan pass on this worktree.

Two M6 release acts cannot be completed by repository code: independent unfamiliar-developer pilots and publication/verification of tagged archives or packages. They remain maintainer-controlled public-release gates. Until they are recorded against a clean candidate commit, describe this as a locally validated beta candidate rather than a completed public release. The successful UI builds currently emit a Node compatibility warning because the validation host has Node 20.11.1 while Vite recommends 20.19+ or 22.12+.

## Product decision

Ship a dependable architecture investigation tool for developers and their agents. A developer should be able to install it from the README, analyze related repositories, identify likely affected callers with evidence, inspect uncertainty, make a change, and refresh the graph without specialist help.

Growth should come from reviewed, reproducible improvements discovered during ordinary development. The host agent may reason about missing patterns; DiffMind remains a deterministic evaluator of source, contracts, configuration, and tested rules. An agent's assertion is a proposal, not a new fact simply because an agent said it.

Public beta scope: single-server local/optional shared workspaces, the existing supported language subset, reliable identity/resource semantics, useful contract inspection for a bounded OpenAPI subset, readable focused views, and a private improvement workflow. Universal framework support, distributed workers, automatic SSO provisioning, arbitrary executable plugins, and guaranteed runtime blast radius are outside this release.

## Evidence behind the plan

A private fresh-install trial successfully installed from docs, discovered the management tools, analyzed 29 repositories in approximately 56 seconds, identified a Java HTTP caller and a Python endpoint-to-cache path, and supported an experimental API change verified by 136 tests. A custom pack was scaffolded, tested, explained, installed and used in a rebuild. These are encouraging operational results, not a precision/recall measurement.

The trial also demonstrated inconsistent freshness, an unresolved URL promoted to a confident external destination, an identity extraction rule that silently returned no values, missing request-field queries, noisy comparisons, poor initial graph fitting, and client reconnection friction. Pack alias propagation and anomalous source-size metrics need diagnosis; their causes are not established. Proprietary source, hostnames and transcripts stay outside this public repository. Reproduce every issue with synthetic fixtures.

## Release invariants

- A known call with an unknown destination remains an unresolved call. It must not create a destination inferred from its filename, function, or display name.
- Every claimed relationship is inspectable through authorized HTTP/MCP access with source location, source revision/content identity, extraction origin, and resolution reason.
- Declared configuration, static code recognition, identity resolution, local reachability and runtime observations are distinct evidence classes.
- Unknown confidence stays unknown. Scores describe a documented heuristic, not calibrated probabilities. Repeated evidence cannot manufacture certainty.
- Repository analysis success, extraction coverage, live freshness and saved-snapshot validity are separate states.
- Historical queries depend only on immutable recorded inputs. Missing old metadata returns an explicit unknown/legacy state.
- The agent can inspect everything it needs in bounded pages. It must not download a whole organization graph to investigate one endpoint.
- Accepted improvements are versioned, tested, auditable and reversible. Public contribution never follows automatically from access to a private repository.

## Delivery order

| Milestone | Outcome | Release role | Depends on |
| --- | --- | --- | --- |
| M0 | Reproduce findings and define a reviewed acceptance corpus | Baseline required | None |
| M1 | Trustworthy facts, freshness and reproducible snapshots | Hard blocker | M0 |
| M2 | Predictable company configuration and pack lifecycle | Hard blocker | M1 fact contract |
| M3 | Endpoint contracts and useful change investigation | Hard blocker for advertised coding-task workflow | M1 |
| M4 | Agent-assisted gap capture and tested local improvements | Hard blocker for self-improvement positioning | M2; use M3 where relevant |
| M5 | Newcomer installation and readable UI/MCP experience | Hard blocker | M1 query contract; can progress alongside M2–M4 |
| M6 | Independent pilot, exact-candidate gates and distribution | Public release gate | M0–M5 |

Deliver each row in small, reviewable changes; keep the main branch releasable. Do not accumulate framework expansion before the trust fixes pass. Assign an engineering owner to each work package and a maintainer to acceptance/release decisions before implementation starts. Estimate effort after M0 reproductions, rather than promising dates from an undiagnosed symptom list.

## M0 — Establish the acceptance baseline

1. Convert every observed failure into a minimal sanitized fixture and a failing behavioral assertion. Include URL expressions, keyword URL arguments, same-path/different-host calls, Redis instances, dirty/untracked checkouts, OpenAPI arrays, old snapshots and fresh client installation.
2. Build a reviewed corpus containing true relationships, explicit non-relationships, expected unresolved facts, local flows and endpoint contracts. Cover existing advertised Go/Python/Java patterns before expanding languages. Include wrappers, queues, environment placeholders, multi-service repositories, versioned routes and cross-team dependencies.
3. Give each case a source revision, expected fact, rationale, reviewer and supported-pattern label. Freeze a holdout subset before changing detectors; do not tune expectations to current output.
4. Add installed-binary tests spanning source -> extraction -> Protocol -> resolver -> saved graph -> viewer HTTP MCP. Unit tests alone missed the unresolved-target propagation failure.
5. Save a machine-readable candidate report keyed by commit, binary digest, corpus version, pack set and platform. Keep previous reports for regression comparison.

Done: each confirmed defect has a reproducible negative test; hypotheses have a diagnostic task rather than a fabricated fix. Precision/recall denominators and unreviewed examples are explicit.

## M1 — Repair the fact pipeline

### M1.1 Unknown targets and confidence

Own the boundary across `internal/extractor/stage/discovery`, extractor artifact writing, `protocol`, workspace artifact hydration, resolver, archgraph and query layers. Trace where unresolved metadata is dropped or overridden before changing it.

Introduce a canonical resolution record with a status such as resolved_internal, known_external, unresolved or ambiguous; candidate matches; reason; and extraction/resolution confidence kept separately. Names are presentation fields, never fallback destination evidence. Ambiguity preserves candidates rather than selecting one; strict graph-build failure may remain configurable for conflicting authoritative identities.

Define relationship aggregation explicitly: confidence reflects the evidence that supports the actual destination and relationship, not an unrelated high-confidence detection. Conflicting evidence appears as conflict; unknown resolution cannot become a fully certain edge. Quality counters derive from these records, including recognized dynamic calls.

Done: the dynamic URL fixture remains searchable and unresolved at every surface, generates no phantom external node, and appears in quality counts. Explicit targets beat route resemblance; resource identity tests and legitimate external URL tests still pass. Verify the original F01–F04 paths through the installed binary again.

### M1.2 Freshness and repository identity

Create a shared repository-analysis status model consumed by sidebar, service cards, HTTP/MCP lists, detail and impact responses. Separate analyzed commit/content digest/time/dirty state from current commit/content state and last check time. Distinguish fresh, changed since analysis, analyzed dirty, unknown/unavailable and failed latest attempt. An unchanged dirty checkout is not automatically obsolete; an old graph must not silently inherit live status as its historical state.

Propagate stable repo IDs into graph service objects. Cache/batch filesystem checks and bound process spawning. Handle missing paths, unavailable Git, branch-only changes, untracked files, edits and reverts. Explain which files influence freshness.

Done: all surfaces report compatible states and timestamps for the same repository/run. Archived snapshots remain valid without live checkout access. A failed refresh does not relabel an old graph as current.

### M1.3 Stable snapshots and comparisons

Capture every graph-affecting catalog, infrastructure, configuration and pack input with its digest at analysis/build time. Historical rebuilds must not consult current source paths. Preserve old graph readers; introduce schema versioning and explicit migration/regeneration guidance before changing stored formats.

Separate semantic facts from observation metadata. Stable keys identify endpoints, dependencies, resources and flows independent of observation time, map iteration, generated IDs and incidental ordering. Normalize only fields whose order is semantically irrelevant. Preserve source/evidence changes in a separate comparison channel and keep meaningful condition/reachability changes visible.

Diagnose added/removed-flow churn independently from timestamp churn. Compare identical input twice, then vary only observation time, source line position, a real target, a flow condition, pack version and schema field.

Done: identical input/config produces zero semantic changes. A line-only move is evidence-only. A target/condition/contract change is semantic. Tests cover old snapshots, interrupted writes and both storage modes. Timing metadata remains auditable.

### M1.4 Coverage and source accounting

Show file counts by analyzed, unsupported, ignored, generated/vendor and failed; recognized-but-unresolved calls; zero-fact repositories; and detector/version coverage. A skipped-file reason must be inspectable. Diagnose the inflated LOC case and align metrics with explicitly stated inclusion rules; validate with virtual environments, node_modules, generated sources, hidden paths and symlinks.

Allow repository classification as service, library, website, infrastructure, tool or documentation, with inference provenance and explicit overrides. Support multiple service identities in a repository only when configured; do not infer that every repo is a deployable service.

Done: operational completion never implies understood integrations. Counts reconcile against fixtures; UI and MCP expose gaps without calling them absent dependencies. Keep coverage percentages separate from measured accuracy.

## M2 — Make company configuration dependable

### M2.1 Shared extraction semantics

Unify or clearly version field-path behavior across identity extraction and relationship detectors: mappings, sequences, wildcard traversal, numeric indices, nulls, duplicate keys, malformed files and multiple YAML documents. Return structured diagnostics for matched/no-values, unsupported path, invalid target, ignored file, conflicting identity and rejected template.

Prefer structured OpenAPI parsing over a generic source regex for server aliases. Handle multiple declared servers with environment/context labels; a declaration is not proof of deployment. Define root/operation server precedence, URL parsing, path stripping, ports and templates. Unbound variables and credential-bearing URLs are rejected or unresolved with a reason. Make environment selection explicit and snapshot it.

Done: the array extraction fixture works as documented; near misses fail safely. Follow an extracted alias through resolution and the saved graph to a query, proving it changes the intended match reason. `pack explain` success alone is insufficient.

### M2.2 Preview, acceptance and rollback

Provide an improvement preview against a pinned baseline: exact added/removed/modified identities and relationships, conflicts, unresolved-count changes, affected repositories, tests and pack digest. Keep candidate rules out of the active graph until activation succeeds.

Use existing pack/config management where possible. Preserve documented override/project/global/builtin precedence and expose the winning rule. Activate atomically, persist the prior lock/config, and rebuild only the affected stages. Return the actual invalidation reason when all repositories must reanalyze. Rollback restores configuration and selects/rebuilds a compatible graph without rewriting history.

Done: positive, negative and full graph tests pass; conflicting priority is explicit; corrupt/tampered packs fail closed; retry is idempotent; activation and rollback survive interruption. Local candidates can be reviewed without network access.

## M3 — Support the coding task, including contracts

### M3.1 Bounded endpoint contract model

Add versioned endpoint contract facts with stable endpoint ID, method/path/server context, request location (body/query/path/header), content type, schema pointer, field name/type/required/nullability/constraints, and exact evidence. Begin with explicitly documented OpenAPI 3.0 local files and local references. Bound recursive references, depth and document size; disable remote fetching by default. Unsupported constructs remain partial, never flattened into misleading certainty.

Keep declared schema, recognized validator behavior and client DTO facts separately sourced. Initial code bindings should cover the exact Python Flask/Cerberus and Java Retrofit/DTO fixture patterns used by the acceptance task, not a claim of complete framework semantics. Multiple schema sources may disagree: report drift, do not silently choose one as runtime truth.

### M3.2 Change-oriented queries

Expose paginated endpoint/contract retrieval, field search and compatibility diff through the shared query layer and MCP. Extend existing tools where coherent; add tools only where discovery remains clear. Return summary/counts/cursors first, with bounded evidence and contract pages. Full-detail mode must also have size limits and a continuation mechanism.

Classify changes with documented request-direction semantics: optional field addition usually compatible; required field addition, accepted-type narrowing and removed fields potentially breaking. A rename is remove+add unless explicit compatibility/alias evidence exists. Do not infer backward compatibility from a similar name or an unchanged service edge.

Return impact tiers: exact endpoint caller; potential service-level caller; unresolved candidate; outside analyzed scope. Include supporting evidence and uncertainty. A caller to another endpoint in the same service must not be presented as definitely broken.

Done: an installed-binary task adds two fields and renames a key; agents retrieve the old/new contract, locate the actual caller, explain the alias policy, and distinguish another endpoint's caller. The application tests, not the graph, establish runtime compatibility. Fields and local references are searchable with source pointers.

## M4 — Let agents improve precision during normal work

### Minimal contribution loop

1. **Observe:** while handling a user task, an agent records a missing fact, incorrect fact, unsupported pattern or contradictory evidence with the graph/run and relevant source identities.
2. **Classify:** choose an existing setting/identity override, a reusable company pack, a generic detector/framework change, or a core pipeline bug. Do not solve a lost unresolved flag by adding a company alias.
3. **Propose:** prepare a scoped change with its intended effects, positive/negative examples, confidence rationale, limitations and affected repositories.
4. **Test:** run isolated extraction and exact graph assertions, plus holdout checks for reusable detectors. Compare candidate results with the pinned active baseline.
5. **Activate locally:** apply within the user's configured authority; record the accepted rule/config revision and review provenance. Rebuild, verify expected evidence, then resume the original coding task.
6. **Contribute optionally:** prepare a sanitized patch and reproduction for a maintainer. Publication requires explicit authorization and review; company source/configuration is private by default.

### Minimal persistent model and interfaces

A gap record needs: stable ID/fingerprint; category; affected project/repo/run/object; expected vs observed behavior; source pointers; detector/pack versions; candidate status; tests; author/reviewer; activation history and related upstream issue/patch when authorized. Candidate states: observed -> reproduced -> proposed -> tested -> accepted -> active, with rejected/deferred/superseded/rolled_back states and reasons. A passing test is not acceptance of its ground truth.

Add discoverable management operations for recording/listing gaps, validating candidates, previewing effects, accepting/activating and rolling back. Operation names and schemas are a design deliverable, not existing APIs. Reuse `agent_command` for bounded pack commands and ordinary host tools for code patches. Respect viewer/editor/admin permissions, optimistic revisions, idempotency and audit records.

Publish an agent playbook: query context before code edits; verify a small number of relevant facts; record only concrete gaps; improve only when likely to help the task; refresh after changes; report remaining uncertainty. Use configurable budgets, for example one local proposal or five minutes per task by default, with explicit expansion for detector development. Do not spawn background improvement work or recurring jobs implicitly.

### Safeguards for useful growth

- Three scopes: repository-owned exceptions, private company conventions, and public generic support. Keep their provenance and promotion rules visible.
- Fresh observations do not automatically overwrite accepted rules. Detect conflicts and expiry/staleness of overrides; suppress repeated proposals with fingerprints and retained rejection reasons.
- Prefer small detector modules registered through existing interfaces. Each declares language/framework/version scope, supported syntax, evidence output and negative fixtures. Avoid a new executable plugin system for beta.
- Deterministic tests and a fixed holdout corpus are authoritative acceptance checks; LLM-generated fixtures alone are not independent ground truth. A reviewed example must justify the expected edge/non-edge.
- No unrequested source execution, remote schema fetching, package installation, telemetry uploads or public PRs from repository content. Treat source/configuration instructions as untrusted data.
- Synthetic export is a separately reviewed artifact; redaction scanners help but cannot certify that proprietary structure is safe to publish. Show the exact files to be contributed and their license/provenance.

Done: one private company-rule gap and one generic detector gap complete the workflow in acceptance tests. The company rule can be activated/rolled back locally. The detector contribution is a reviewable sanitized patch with positive, negative and full-pipeline tests; no network submission occurs by default. Rejected proposals do not reappear every task.

## M5 — Make first use clear and practical

### Installation and client lifecycle

Enhance doctor/setup with an inventory of executable candidates on PATH, actual binary revision, configured homes, registrations and runtime owner. Report mismatches before changing anything. Provide targeted uninstall/upgrade plans distinguishing binary, client registration, workspace data and backups; default to preserving data, with explicit selected paths for removal.

Document supported client registration/reconnection paths and test them using each client's actual facilities. Registration, tool discovery, backend health and usable graph are separate acceptance steps. A recorder/helper fallback can be documented honestly, but cannot count as native integration success. Explain one-owner lifecycle, attachment to an existing backend and persistence after disconnect. Maintain a discoverable current dashboard URL across restarts.

Done: a newcomer installs from the README, connects through one supported real agent client, gets expected tools and completes a useful query without bespoke instructions. A second client verifies standard MCP interoperability. Duplicate installations, reconnect, upgrade and owner-crash cases have tested behavior. Windows remains explicitly unsupported.

### UI and response design

Default to a small service/endpoint neighborhood for large projects. Search should focus the requested object, not expand an entire team unexpectedly. Offer team/context expansion, fit-to-visible nodes, persistent zoom, bounded resource aggregation, readable labels and an empty/partial/error state that explains next actions.

Use one primary Update graph action, with import and advanced analysis/build steps clearly separated. Keep warnings in layout; show coverage/freshness inline and link to evidence. Provide first-class views for contracts, relevant callers, gap proposals and candidate graph changes.

Done: browser QA at desktop and narrow widths checks long labels, large graphs, expanded warnings and ingestion progress. No overlap or clipped primary controls. A developer can identify the changed endpoint, inspect one caller and open evidence without zoom hunting. Query responses default to a proposed 32 KiB budget, with explicit truncation/cursors, tested against large services; choose final limits from pilot measurements.

## M6 — Validate the product and release it

### Staged pilot and acceptance thresholds

Run a small pilot with at least three developers unfamiliar with the implementation, across more than one language combination. Each starts with the README and performs one real coding task. Record intervention points, installation/discovery failures, time to first source-backed result, incorrect claims, useful findings, missed facts and whether the agent completed a tested improvement. Keep company evidence local unless explicitly shared.

Public-beta gates proposed for this scope:

- All critical trust and authorization regression tests pass on the installed candidate; no known unresolved-to-confident promotion or evidence-access failure remains.
- No false-positive relationships on the fixed adversarial release corpus. Report TP/FP/FN and unresolved counts by supported pattern, not an organization-wide accuracy score. Suggested supported-pattern holdout targets: at least 98% precision and 90% recall, with raw counts/sample sizes; small samples do not justify general claims. Missed advertised baseline patterns are blockers even if an aggregate target passes.
- Same-input semantic graph comparison is stable; historical reads work after source paths disappear or change; artifacts round-trip across the supported schema transition.
- Every pilot developer completes installation, real client connection, source-backed investigation and a tested code task from published docs after fixes. A failed attempt remains recorded and must be rerun; do not relabel assisted setup as independent success.
- At least one tested company configuration improvement demonstrably changes the intended persisted graph outcome and can be rolled back.
- Performance reports state hardware, input/file counts, cold/warm state, pack changes, latency and peak memory. No scalability guarantees from one checkout sample. Investigate pathological behavior and document supported practical limits.

### Candidate and distribution checklist

Freeze a specific commit; select an intentional beta version. Run make verify and the applicable dependency/security checks, both UI suites/builds, pack tests and all four native installation/acceptance gates on that exact candidate. Re-run relevant gates whenever the candidate changes. Review schema/backup compatibility and upgrade/rollback notes.

Verify actual release archives after publication by independent download/checksum/install, then test the pinned Homebrew formula before promotion. Run real container health/persistence/restart and shared-access/restore drills before advertising shared deployment as beta-certified. If these drills are deferred, explicitly restrict the first beta to local use.

Publishing tags/assets, promoting packages and external announcements are maintainer-authorized steps. Draft release notes must list tested patterns, known limits, upgrade guidance and one sanitized end-to-end demonstration. Avoid claims of authoritative architecture or guaranteed blast radius.

## Suggested implementation slices

| Order | Reviewable change | Main acceptance proof |
| --- | --- | --- |
| 1 | Synthetic full-path regressions and release report harness | Reproduce trial failures without company data |
| 2 | Canonical unresolved/resolution/confidence propagation | No fabricated destination over viewer MCP |
| 3 | Unified repo identity/freshness and coverage accounting | Same states in UI/HTTP/MCP; accurate file counts |
| 4 | Snapshot input capture and semantic/evidence diff separation | Stable replay; precise single-change tests |
| 5 | Pack extraction diagnostics and structured alias propagation | Rule -> identity -> match -> persisted evidence |
| 6 | Candidate preview/activation/rollback | Atomic activation and reproducible rollback |
| 7 | Bounded OpenAPI contract model and query pagination | New fields and caller bindings retrievable |
| 8 | Compatibility/endpoint-impact queries | Alias-aware diff, no unrelated-endpoint certainty |
| 9 | Gap records, agent playbook and contribution scaffolding | Private rule + sanitized detector lifecycle |
| 10 | Setup diagnostics/reconnection and focused dashboard | Independent first-use and browser acceptance |
| 11 | Pilot fixes and exact-candidate release certification | Signed-off evidence checklist, not a readiness claim |

Cross-cutting completion rule: each slice includes contract/schema compatibility, focused behavior tests, installed-binary tests where a pipeline boundary matters, updated public docs/support matrix, and measured limitations. No proprietary fixtures in public tests. Mark completed only with the implementing commit and verification evidence.

## After beta: grow from measured gaps

Prioritize contributions by recurrence across independent projects, user impact and precision risk, not framework popularity alone. Stabilize a small set of useful packs and detector fixtures first. Add opt-in local gap summaries and maintainer triage without uploading source automatically. Track accepted proposals that improve holdout results, false-positive regressions, rollback frequency and task interruption cost.

Later milestones can add more contract formats, framework bindings, monorepo ownership, richer environment modeling and signed pack distribution. Distributed workers, broader identity integration, cloud backups and Windows remain separately scoped projects. Do not make them prerequisites for a trustworthy local beta.
