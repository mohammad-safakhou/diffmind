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
- **LLM-only objectives are high-variance run-to-run.** Objectives with no
  deterministic detector (`queue_publish`, `cache_operation`, `outbound_rpc`,
  `stream_consume`, `command_exec`, `rpc_endpoint`, `webhook`, `cli_command`)
  get a single whole-repo "find ALL X" call — the highest-variance prompt shape.
  Measured on `routing-service` across two runs: `cli_command` 7→0,
  `outbound_rpc` 14→10, `command_exec` 1→0, `stream_consume` 0→1, while the
  deterministic-floored types (http_route, scheduled_job, db_operation) stayed
  stable. This is the single biggest remaining accuracy lever — see roadmap.
- **Connections have no LLM tail.** The connection stage is 100% deterministic
  AST BFS, so exposures wired via DI / events / async-queue / dynamic dispatch /
  deep chains (>`MaxDepth`) or on non-JVM stacks silently get no connections.

Fixed June 2026 (previously listed here): deterministic db-table precision nits
— `entity_manager` and `*_id_seq`/`*_seq` are now filtered out, and `${...}`
config placeholders in queue-consumer names resolve to the real queue. A
residual `unknown <table>` can still appear when the *LLM itself* names a db op
that way (we never rewrite LLM-authored names); reconcile collapses it by
`(resource, operation)`, so it is not a duplicate.

## 6. Roadmap / milestones

DONE (June 2026):
- ✅ **Accuracy eval harness** — `internal/eval` + `diffmind eval`. Scores
  exposures/deps/connections against hand-labeled fixtures with per-objective
  P/R/F1, matching on the same identity the pipeline dedups with. Cheap mode
  (deterministic floor, hermetic, `go test ./internal/eval/...`) is the CI
  guardrail; `score-run` grades a real run dir. (Partially covers the old
  "stability regression harness" item — accuracy now; K-run variance pending.)
- ✅ **Junk-table filter** for the deterministic db deriver (`entity_manager`,
  `*_id_seq`/`*_seq`, generic JPA handles).
- ✅ **Config placeholder resolution** — `${...}` queue/topic names in framework
  bindings resolve to the real resource
  (`internal/stage/discovery/config_resolve.go`).

Near-term (the highest-leverage gaps, in priority order):
1. **LLM connection verify/repair pass** — add an LLM *tail* after the
   deterministic AST connection walk for exposures it leaves unconnected
   (DI / events / async-queue / dynamic dispatch / deep chains / non-JVM),
   constrained to the known dependency catalog and AST-validated. Connections
   are the hardest sub-problem and currently have zero LLM coverage — the
   clearest violation of the "LLM is the brain" thesis. (`--max-catalog-items`
   is still wired; the removed scaffolding is recoverable from git `3c59dee~1`.)
2. **Stabilise LLM-only objectives** — keyword-seed candidate files (reuse the
   `objectiveMatchers` keyword lists) so `queue_publish`, `cache_operation`,
   `outbound_rpc`, `command_exec`, etc. get evidence-gated directory shards
   instead of one whole-repo call. Directly attacks the run-to-run variance
   documented in §5. Gate on the existing soft-target so no empty fan-out.
3. **Promote detail-discovered dependencies** — the detail stage observes
   downstream ops (Redis/SQS/HTTP/exec) it currently discards as text; route
   genuinely-new ones back through verification (preserving "discovery is the
   authority") instead of dropping them.
4. **Extend deterministic discovery to the remaining types** — queue_publish
   (template `.send`), cache_operation (`@Cacheable` w/ external store),
   outbound_rpc — each gated on a precision check before promotion.
5. **Deterministic db coverage beyond JVM** — Django ORM, ActiveRecord, GORM,
   Sequelize/Prisma. The biggest lever for non-Java repos.

Medium-term (quality & trust):
6. **Pin LLM determinism** where the provider supports it (seed/temperature);
   measure variance with the eval harness's planned K-run/full mode, don't assume.
7. **Cost guardrail** — optional token budget that aborts and fails (not
   silently completes).

Longer-term (product):
8. **Cross-service graph** — stitch many services' exposures↔dependencies into
   a fleet-wide architecture graph (e.g. service A's outbound_http → service B's
   http_route). (Placeholder resolution above makes the instance names stable
   enough to stitch on.)
9. **Diff mode** — architecture delta between two commits/versions.

## 7. Where things live

- `internal/objectives/registry.go` — the objective map + prompts.
- `internal/pipeline/` — orchestration, lifecycle, resume, events, and the
  deterministic-floor projection used by eval.
- `internal/stage/` — stage-owned extraction logic.
- `internal/extraction/`, `internal/llmrun/`, `internal/runstate/`, and
  `internal/entitykey/` — domain contracts, LLM runtime, persisted checkpoints,
  and canonical identities. See `docs/ARCHITECTURE.md`.
- `internal/ast/` — tree-sitter engine; `framework/` — per-framework detectors.
- `internal/stage/reconcile/` — final deduplication, normalization, sorting,
  and orphan filtering. `internal/entitykey/` owns the identity reused by eval.
- `internal/eval/` — golden-set accuracy harness; fixtures + label format under
  `testdata/eval/` (see its `README.md`).
- `internal/ui/` — dashboard server + SPA (`web/`).
- `cmd/diffmind/` — CLI (`run`, `retry`, `validate`, `list-runs`, `eval`, `ui`).
- Artifacts: `~/.diffmind/runs/<run_id>/` (manifest, exposures, dependencies,
  connections, unresolved, prompts, events.jsonl, state/).

## 8. Validating a change

`go build ./... && go test ./...` must stay green. Two complementary checks:

1. **Eval harness (fast, objective, no OpenCode).** `go test ./internal/eval/...`
   or `diffmind eval --mode cheap` scores the deterministic floor against
   labeled fixtures (`testdata/eval/`) — a regression in any deterministic stage
   drops a fixture's F1. Add/extend a fixture when you touch a detector or ORM
   deriver. To grade a full LLM run, `diffmind eval --mode score-run --run <id>
   --fixture <dir>`. The matcher keys on `entitykey.Semantic(Loose)`, so it
   judges "correct" exactly as the pipeline judges "duplicate".
2. **Real-run diff (behavioral).** Run a real extraction (see README) and compare
   artifacts + per-objective counts against a prior run in `~/.diffmind/runs/` —
   watch for **stability** (counts not swinging run-to-run), **no silent
   over-merge** (distinct datastores/tables preserved), and resolved names (no
   `${...}` placeholders, no `entity_manager`/`*_seq` junk), not just a smaller
   number. Note that LLM-only objectives (§5) still swing run-to-run until the
   roadmap-#2 sharding lands — judge those against a label, not a single run.
