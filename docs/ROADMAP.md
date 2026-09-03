# Diffmind product roadmap

This is the current product checklist. Earlier plans under `docs/extractor/docs`
are historical and do not define the current architecture or backlog.

## Readiness checkpoint — 2026-09-03

The implemented product is a **single-server developer/team platform**. The
completed batches below cover installation, deterministic extraction, teachable
patterns, graph/UI/MCP queries, shared access, continuous refresh, resource caps,
and offline recovery with managed backup rotation. Every completed implementation
batch is committed; a completed local check is not a claim of a published release.

This roadmap is not entirely complete. The remaining items below are explicit
engineering projects or deployment/release gates, not silently dropped features.
See [verification evidence and limits](readiness-verification.md) for the latest
local checkpoint, including native-platform and deployment checks still pending.

## Goal

A developer gives an agent repository scope and access. The agent installs and
operates Diffmind, creates/configures projects, imports repositories, builds and
maintains an evidence-backed graph, and queries it over MCP. Manual CLI/UI paths
remain alternatives, not requirements. Teams run the same core on a shared server. No LLM is required
for extraction; conventions are supplied through tested configuration and packs.

## Delivered foundation

- [x] One repository and command; release installer and installation doctor.
- [x] Local-directory and GitHub organization import with one-click graph build.
- [x] Deterministic extraction, graph exploration, historical graph run pinning.
- [x] Read-only HTTP query API, stdio MCP, and remote HTTP MCP.
- [x] Knowledge-pack authoring, fixtures, install/lock verification, service
  identity overrides, and configuration-based relationship resolution.
- [x] Shared Compose deployment, scheduled refresh, trusted-proxy roles, audit
  logging, and CI/security checks.

## Agent-first management batch

- [x] Agent-executed source bootstrap with isolated installation verification and
  machine-readable full-management MCP registration; no user CLI/UI chores.
- [x] `diffmind agent` owns automatic backend startup, stop/restart, persisted
  refresh/worker settings, maintenance coordination, and crash cleanup.
- [x] Discoverable management catalog covers browser mutations, preserving API
  validation, permissions, quotas and mutation auditing; viewer access stays read-only.
- [x] Agent pack authoring/validation/install and offline backup/storage commands
  execute through a bounded local interface, not an arbitrary shell.
- [x] Real-binary stdio acceptance from an empty home through graph, incremental
  refresh, packs, backup, SQLite, scheduling, reconnect and owner-crash cleanup.

Host-client registration/installation approvals and company credentials still
require the user's authority. Agent-owned local lifecycle is not an always-on
system service; company instances use the shared deployment model.

## Real-company readiness batch

- [x] Incremental analysis keyed by source revision/content, configuration,
  verified pack content, analyzer identity, and run options.
- [x] Verify saved artifacts before reuse; explicit full rebuild escape hatch.
- [x] Persist repository progress and successful-analysis checkpoints.
- [x] Cancel ingestion, analyzer child processes on macOS/Linux, and graph builds.
- [x] Resume interrupted jobs on startup; manual resume/retry for failed,
  partial, or cancelled jobs. Revalidate inputs before checkpoint reuse.
- [x] Real-binary Go/Python/Java acceptance fixture: exact relationships, source
  evidence, HTTP/MCP queries, incremental updates, artifact invalidation, retry.
- [x] Current architecture/operations docs and historical-plan notices.

The automated fixture is a baseline, **not universal architecture accuracy**.
An actual company pilot still needs a developer to compare discovered edges
against known relationships and contribute synthetic reproductions of gaps.

## Remaining engineering and release gates

- [ ] Organization-scale metadata storage and distributed workers: execution
  fencing/leases, worker RPC, shared artifact storage, multi-replica authorization,
  upgrade/recovery compatibility and fault/load tests. SQLite queue indexing is
  delivered; it does not supply this distributed architecture.
- [ ] Identity-provider group synchronization and user-bound personal tokens:
  explicit provider/group mapping, revocation/provisioning semantics and tests.
  Current explicit memberships and independent agent tokens remain supported.
- [ ] Application-schema migration and path relocation, with typed migration
  plans, dry-run reports, rollback and byte/timestamp preservation tests. Current
  recovery restores original paths; queue migration is the only schema migration.
- [ ] Storage-provider integration: select the provider and implement encrypted
  off-host copies, credential handling, lifecycle/immutability and restore drills.
  Local managed retention/systemd scheduling are delivered below; cloud storage,
  zero-downtime backups and Compose/launchd scheduler adapters are not.
- [ ] Execute the four native CI release checks on the committed candidate,
  choose a release version, publish its tag/assets, verify downloads and promote
  the generated pinned Homebrew formula. Local macOS ARM64 validation is not
  evidence for the other platforms or for a published release.
- [ ] Run a real-company accuracy/operations pilot against known relationships;
  contribute sanitized pattern gaps. Synthetic tests cannot certify every stack.

Windows and additional package ecosystems remain unsupported extensions, not
partially working advertised installation paths. No remote release/tag push,
timer installation, live company integration or infrastructure provisioning is
performed implicitly by completing local repository work.

## Teachable-pattern batch

- [x] Declarative, opt-in HTTP/RPC/queue detectors with file/line provenance.
- [x] YAML/JSON wildcard paths and named-target regex rules; explicit limits,
  input errors, fixture confinement, and sanitized skip diagnostics.
- [x] Pack detections and HTTP/RPC resolver results feed the persisted UI/MCP graph.
- [x] Exact identity/detection/evidence tests and production graph-level fixtures.
- [x] Service-manifest and Spring Cloud OpenFeign configuration packs; scaffold
  with an immediately runnable multi-repository graph test.
- [x] [Tested support matrix](supported-patterns.md) and contributor guidance.

This is an extensible baseline, not complete framework coverage. Arbitrary AST
plugins, profile/environment evaluation, and runtime reachability from configured
URLs are not provided by these packs. More synthetic patterns remain welcome.

See [architecture](ARCHITECTURE.md), [ingestion operations](ingestion.md),
[company deployment](company-deployment.md), and [contributing](../CONTRIBUTING.md).

## Graph history and tracing batch

- [x] Explicit saved-run comparison across services, objects, local flows,
  resources, external systems, and typed relationships.
- [x] Deterministic semantic identities, occurrence preservation, before/after
  evidence, changed fields, pagination, and repository/pack provenance context.
- [x] Read-only HTTP and MCP history/comparison/path/exact-ID trace queries.
- [x] Directed shortest dependency paths with cycle guards and explicit limits;
  partial local traces never imply cross-service runtime execution.
- [x] Comparison screen with historical selectors, shareable pinned URLs,
  direction swap, evidence expansion, and change pagination.
- [x] Core, authenticated HTTP/MCP parity, pack-driven graph comparison, frontend
  helper tests, and browser smoke verification.

[Contract and examples](graph-history.md). Comparisons explain which saved facts
changed, not their cause. Source-code diffs, inferred causal explanations, and
proof of end-to-end execution are not included.

## Continuous operations batch

- [x] Persisted refresh queue shared by manual, scheduled, and webhook triggers.
- [x] Bounded project workers and a global sync/analyzer concurrency budget;
  fixed-size per-project repository worker pools and busy-project deferral.
- [x] Durable job attempts, bounded retry/backoff, cancellation intent, shutdown
  draining, and interrupted-attempt recovery without erasing timestamps.
- [x] Ingestion history for direct imports and refreshes, including earlier
  attempts; atomic checkpoint replacement across the filesystem store.
- [x] Opt-in signed GitHub push webhooks, configured repository/branch filtering,
  durable delivery deduplication, payload limits, and queue backpressure.
- [x] Authenticated metrics, viewer-safe operation history, editor controls,
  an operations screen, and deployment configuration/documentation.
- [x] Real-binary queued incremental refresh, concurrency/recovery/security
  regression tests, and browser checks of operation controls/history.

See [continuous operations](operations.md). This remains a single-process
queue (JSON or opt-in SQLite) with at-least-once execution—not a distributed scheduler
or an unlimited-scale job database. No automatic history pruning is enabled.

## Recovery and distribution batch

- [x] Versioned offline create/verify/restore, file/archive checksums, byte/entry
  limits, unsafe-archive rejection, and non-overwriting restore publication.
- [x] Cross-process maintenance leases; preserved graph, repository and job/
  ingestion history, file bytes/modes, and original record timestamps.
- [x] Real-binary historical-graph/attempt recovery drill; corruption, traversal,
  symlink, future-format and cross-process lock tests.
- [x] Same-repository Homebrew development formula and pinned release-formula
  generation from four platform checksums; CI/release checks.
- [x] Non-overwriting synthetic demo, contributor walkthrough, contribution
  lanes, pattern-gap issue form, PR checklist, and distribution checks.

See [recovery](backup-recovery.md), [distribution](distribution.md), and
[contributor quickstart](contributor-quickstart.md). Archives are unencrypted;
paths are not migrated. Recipes are implemented, not proof a release has been
published or installed on every target platform.

## Project access batch

- [x] Opt-in scoped project memberships with explicit viewer/editor grants,
  proxy-role ceilings, admin recovery, and legacy compatibility.
- [x] Atomic revision-checked policies, validation, restart persistence and
  backup preservation; corrupt policies fail closed.
- [x] Server-side project/record authorization, filtered discovery and job
  pagination, hidden-project denial, and admin-only host configuration.
- [x] Per-request remote MCP identity and project filtering across every tool;
  revocation also closes live event streams.
- [x] Admin membership screen, effective-role workspace/operations controls,
  and non-admin empty-project onboarding without a create prompt.
- [x] HTTP/MCP isolation, role changes, concurrency, traversal, recovery,
  frontend helper and browser smoke checks.

See [project access](project-access.md). This is application-level authorization,
not an OS sandbox or distributed multi-tenant platform. Shared server tokens
and local stdio MCP retain trusted full access. Scoped agents can use the identity
proxy or the project tokens below. Automatic group provisioning and personal
tokens tied to user membership are not included.

## Indexed queue persistence batch

- [x] Opt-in SQLite jobs/attempt indexes; transactional admission, deduplication,
  claims and updates, with one-active-job-per-project database enforcement.
- [x] Shared JSON/SQLite lifecycle, indexed polling, permissions-filtered job
  pagination, and aggregated metrics.
- [x] Offline versioned migration/verification, original JSON byte and timestamp
  preservation, atomic no-replace activation, and fail-closed backend selection.
- [x] Local CLI single-server lock that allows analyzer children/read-only MCP.
- [x] Backend parity, subprocess contention/crash rollback, corruption, index
  plans, migration/backup recovery, and real-binary company acceptance.
- [x] Reproducible 10,000-job history benchmark and migration/rollback guide.

See [queue storage](queue-storage.md). Project/ingestion/graph metadata has not
been moved into the database. Distributed leases, execution fencing, worker RPC,
shared artifact storage and multi-replica deployment remain future work.

## Project-scoped agent credential batch

- [x] Admin-issued single-project viewer/editor tokens with mandatory expiry,
  independent service grants, hash-only storage and one-time secret return.
- [x] Atomic private registry, durable idempotent revocation, bounded validation,
  retained creation/revocation history and fail-closed corruption handling.
- [x] HTTP/MCP project isolation and identity switching; no stronger-identity
  fallback, legacy-mode rejection, live-stream expiry/revocation checks.
- [x] Admin issuance/history/revocation screen, secure response caching policy,
  stable mutation audit actors, and agent/rotation/offboarding guidance.
- [x] Store, HTTP, every MCP tool, route isolation, concurrency, frontend and
  real-analyzer graph/backup recovery tests for JSON and SQLite deployments.

See [agent tokens](agent-tokens.md). Tokens are independent service identities,
not personal tokens or SSO/group provisioning. Project resource limits are
delivered below, as is managed backup rotation. Distributed workers, cloud backup
integration and release publication remain open.

## Per-project resource limit batch

- [x] Persisted, revision-checked pending-job and repository-operation caps;
  zero inherits the global ceiling without changing existing deployments.
- [x] Atomic JSON/SQLite admission for all triggers and explicit retries;
  accepted deduplication and original attempt history/timestamps preserved.
- [x] Combined global/project sync-and-analysis limiter; saturated projects do
  not reserve global slots, updates wake waiters and reductions drain safely.
- [x] Authorized usage API and admin-only controls in both access modes;
  validated drafts, conflict reload, fail-closed policies and retryable overflow.
- [x] Concurrency, cancellation, direct actions, fleet/webhook, permission,
  frontend, and real Go/Python/Java plus offline-recovery acceptance tests.

See [project resource limits](operations.md#per-project-resource-limits). These
are single-server admission/concurrency caps, not distributed scheduling,
reserved throughput, OS isolation, CPU/memory quotas or automatic retention.

## Managed backup lifecycle batch

- [x] Explicit `backup rotate --offline --directory ... --keep-last N` and verified
  catalog listing; private, workspace-bound, versioned catalog/receipts.
- [x] Independent archive verification before atomic publication and pruning;
  keep-last retention removes only catalog-owned backup snapshots, never live
  records or Git history. Corruption and concurrency fail safely.
- [x] Operator-installed Linux systemd timer and maintenance helper: bounded
  stop/backup/restart, prior-state preservation, interruption and post-stop
  recovery intent, with explicit deployment/monitoring guidance.
- [x] Store/CLI/maintenance lifecycle tests, corruption/symlink/concurrency cases,
  and real Go/Python/Java graph/token/quota/queue recovery on JSON and SQLite.

See [backup automation](backup-automation.md). Scheduling creates a maintenance
window. The timer is not enabled automatically; actual host integration and
off-host/encrypted backup storage remain deployment responsibilities.

## Native release validation batch

- [x] Reusable native archive verifier: private installer environment, exact
  version/platform, embedded dashboard assets, SQLite startup, and the full
  graph/HTTP/MCP/incremental/managed-recovery acceptance suite against the
  **installed release binary**, not a substitute analyzer build.
- [x] Native macOS/Linux amd64/arm64 CI candidate matrix and mandatory per-platform
  checks before release artifacts reach the publication job.
- [x] Neutral, reproducible archive packaging without local ownership metadata,
  macOS resource forks or xattrs; unsafe/non-neutral archives fail validation.
- [x] Archive-shape and environment-isolation regression tests; local macOS ARM64
  candidate installation/acceptance check.

See [release maintenance](distribution.md). CI configuration is delivered;
executing the other three platform checks, publication and pinned-formula
promotion are still unchecked release gates above.
