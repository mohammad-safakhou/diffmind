# DiffMind — Platform, Goals & Roadmap

> Read this first if you're a new contributor (human or AI session). It captures
> *what DiffMind is, why it's built the way it is, and where it's going* — so you
> don't have to reconstruct the goals from the code every time.

---

## 1. What DiffMind is

DiffMind is a **multi-language architecture extractor**. Point it at a service's
source repository and it produces structured JSON describing the service's
**externally observable architecture**:

- **Exposures** — how the service can be triggered from outside: HTTP routes,
  webhooks, RPC endpoints, queue/stream consumers, scheduled jobs, CLI/Lambda
  entrypoints.
- **Dependencies** — what the service reaches out to: database operations,
  cache operations, outbound HTTP/RPC calls, queue/topic publishes, stream
  consumption, external command execution.
- **Connections** — for each exposure, the conditional call-path(s) to the
  dependencies it triggers, with per-hop conditions and repetition derived from
  control flow.

The output is a high-level, evidence-backed map of "what comes in, what goes
out, and how they're wired" — for documentation, impact analysis, dependency
graphs, and migration/audit work across a fleet of services.

## 2. What we offer (the value)

- **Language-agnostic.** Works across Java/Kotlin, Python, JS/TS, Go, and more,
  because the semantic understanding is done by an LLM, not per-language rules.
- **High-level, not a symbol dump.** One "reads `orders`" dependency, not 15
  repository methods. The output is at the altitude an architect thinks in.
- **Evidence-backed.** Every item carries source locations and snippets.
- **Deterministic where it can be.** Connections and a growing share of
  discovery are pure static analysis — stable, fast, free.
- **Headless & fleet-ready.** Runs against an OpenCode server with an isolated
  per-run snapshot; a watchdog keeps unattended runs from deadlocking.

## 3. Core design principle

> **The LLM is the brain. Deterministic static analysis is the skeleton and the
> memory.**

Enterprise codebases are full of custom frameworks, reflection, dynamic
registration, and in-house abstractions. No deterministic analyzer will ever
understand all of that across many languages — that semantic understanding is
exactly what LLMs (trained on billions of lines) are uniquely good at. So the
**LLM stays the authority on what exists.**

Deterministic static analysis (a tree-sitter AST index) is not a competing
extractor. It has three jobs, all in service of the LLM:

1. **Recall floor** — guarantee the mechanical majority is always found, so
   output never silently drops below a known baseline.
2. **Focus** — feed what we already know into the LLM's context
   (`KNOWN_CONFIRMED_ITEMS`) so it spends its tokens on the custom tail, not on
   re-deriving the boring 80%.
3. **Verification** — confirm claims against real source positions.

Why this matters: LLM output variance scales with how big and ambiguous the ask
is. Asking "find ALL db operations in the whole repo" is the highest-variance
prompt possible. Shrinking that surface (deterministic floor + tight, evidence-
scoped shards + strong dedup) is what makes the system **stable and affordable**
without giving up the LLM's reach.

## 4. Pipeline (one path, no modes)

```
repo_facts → ast_index → deterministic_discovery → LLM discovery (merged)
           → reexamination → detail (additive) → connections → reconcile → artifacts
```

- **repo_facts** — one LLM call + marker-file scan: languages, frameworks,
  build/config files. Used to scope prompts to the real stack.
- **ast_index** — tree-sitter symbols, call graph, framework bindings, config.
- **deterministic_discovery** — high-precision exposures/deps from the AST;
  always runs; seeds the LLM and is merged into the final set.
- **LLM discovery** — per-objective agents, grounded in advisory AST hints,
  sharded only where there is real static evidence (see below).
- **reexamination** — re-confirms low-signal candidates.
- **detail** — enriches each entity; **strictly additive** (never drops or
  re-identifies a discovered entity).
- **connections** — deterministic BFS over the call graph; zero LLM.
- **reconcile** — dedup to the high-level fact, sort for stable IDs, drop
  orphaned connections.

### Key invariants (don't regress these)

- **One canonical run.** No off/observe/shadow/active modes. Deterministic
  discovery always runs and is always merged. (Removed June 2026.)
- **Prompts are language-scoped.** Never re-introduce cross-language pattern
  noise on a single-language repo.
- **Sharding is evidence-gated.** Shard only directories with real AST
  candidates; never fan an empty objective into N whole-repo scans. A shard
  scopes what it *reports*, never what it may *read* (shared base classes/config
  must stay visible).
- **Detail is enrichment, not a gate.** It may add to discovery, never subtract.
- **Dedup targets the architectural fact.** db/cache collapse by
  `(resource, operation)`; distinct real datastores are preserved.

## 5. Known limitations / honest gaps

- **Deterministic `db_operation` is JVM-only.** It recognises Spring Data / JPA
  / MyBatis (`*Repository`, `*Dao`, `EntityManager`). On other stacks it returns
  nothing and `db_operation` falls back to LLM-only discovery — safe, but not
  yet stabilised there.
- **Deterministic coverage is partial.** Stable today: http_route,
  queue_consumer, scheduled_job, outbound_http (Feign), db_operation (JVM).
  Still LLM-only: queue_publish, cache_operation, outbound_rpc, stream_consume,
  command_exec, rpc_endpoint, webhook, cli_command.
- **LLM sampling is not pinned.** No temperature/seed control yet (provider may
  not honor it on the configured model); residual run-to-run variance is damped
  by the deterministic floor + dedup, not eliminated.
- **Resource-name normalization is best-effort English** (singular/plural). It
  errs toward "don't merge", never a wrong merge.
- **No cost/budget guard.** A run will spend whatever it spends; there is no
  token ceiling or abort-on-exhaustion (deliberately deferred).
- **Minor precision nits** in deterministic db tables (e.g. `entity_manager`,
  `*_id_seq`) — low-signal, collapsed by reconcile, not duplicates.

## 6. Roadmap / milestones

Near-term (stability & coverage):
1. **Extend deterministic discovery to the remaining types** — queue_publish
   (template `.send`), cache_operation (`@Cacheable` w/ external store),
   outbound_rpc — each gated on a precision check before promotion.
2. **Deterministic db coverage beyond JVM** — Django ORM, ActiveRecord, GORM,
   Sequelize/Prisma. This is the biggest lever for non-Java repos.
3. **Junk-table filter** for the deterministic db deriver (`entity_manager`,
   `*_id_seq`, sequences).

Medium-term (quality & trust):
4. **Pin LLM determinism** where the provider supports it (seed/temperature);
   measure variance, don't assume.
5. **Stability regression harness** — run discovery K times on a fixture repo,
   report per-objective variance; gate changes on it.
6. **Cost guardrail** — optional token budget that aborts and fails (not
   silently completes).

Longer-term (product):
7. **Cross-service graph** — stitch many services' exposures↔dependencies into
   a fleet-wide architecture graph (e.g. service A's outbound_http → service B's
   http_route).
8. **Diff mode** — architecture delta between two commits/versions.

## 7. Where things live

- `internal/objectives/registry.go` — the objective map + prompts.
- `internal/agents/` — orchestrator (`pipeline.go`), discovery, sharding,
  grounding, deterministic discovery, detail, connections, watchdog, liveness.
- `internal/ast/` — tree-sitter engine; `framework/` — per-framework detectors.
- `internal/reconcile/` — final dedup / sort / orphan-drop.
- `internal/ui/` — dashboard server + SPA (`web/`).
- `cmd/diffmind/` — CLI (`run`, `retry`, `ui`).
- Artifacts: `~/.diffmind/runs/<run_id>/` (manifest, exposures, dependencies,
  connections, unresolved, prompts, events.jsonl, state/).

## 8. Validating a change

`go build ./... && go test ./...` must stay green. For behavior changes, run a
real extraction (see README) and compare the artifacts and per-objective counts
against a prior run in `~/.diffmind/runs/` — watch for **stability** (counts not
swinging run-to-run) and **no silent over-merge** (distinct datastores/tables
preserved), not just a smaller number.
