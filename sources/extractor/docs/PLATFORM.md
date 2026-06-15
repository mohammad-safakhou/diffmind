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
           → reexamination → deterministic conversion/backfills
           → connections → connection_repair → reconcile → artifacts
```

- **repo_facts** — one LLM call + marker-file scan: languages, frameworks,
  build/config files. Used to scope prompts to the real stack.
- **ast_index** — tree-sitter symbols, call graph, framework bindings, config.
- **deterministic_discovery** — high-precision exposures/deps from the AST;
  always runs; seeds the LLM and is merged into the final set.
- **LLM discovery** — per-objective agents, grounded in advisory AST hints,
  sharded only where there is real static evidence (see below).
- **reexamination** — re-confirms low-signal candidates.
- **conversion/backfills** — converts verified discovery seeds directly to
  entities, then adds high-precision AST-derived auth, inputs, datastore
  platform, dependencies, and client-instance propagation.
- **connections** — deterministic BFS over the call graph.
- **connection_repair** — one constrained, fail-soft LLM tail for exposures
  left unconnected, choosing only from the existing dependency catalog.
- **reconcile** — dedup to the high-level fact, sort for stable IDs, drop
  orphaned connections.

The LLM detail stage was deleted in June 2026. It consumed roughly one third of
run tokens, discovery already owned every identity-bearing field, and much of
its extra prose never reached the graph. Richness that matters to the product
must now be produced by discovery or by high-precision deterministic backfills.

### Key invariants (don't regress these)

- **One canonical run.** No off/observe/shadow/active modes. Deterministic
  discovery always runs and is always merged. (Removed June 2026.)
- **Prompts are language-scoped.** Never re-introduce cross-language pattern
  noise on a single-language repo.
- **Sharding is evidence-gated.** Shard only directories with real AST
  candidates; never fan an empty objective into N whole-repo scans. A shard
  scopes what it *reports*, never what it may *read* (shared base classes/config
  must stay visible).
- **Discovery is the entity contract.** There is no later semantic repair pass
  for names, details, evidence, or inputs. Discovery output must be usable as-is.
- **Dedup targets the architectural fact.** db/cache collapse by
  `(resource, operation)`; distinct real datastores are preserved.
- **Absence is an empty list, never an entity.** Placeholder/no-result sentinel
  rows are invalid even when they satisfy the JSON schema.

## 5. Known limitations / honest gaps

- **Deterministic coverage is partial.** Stable today: http_route,
  queue_consumer, scheduled_job, outbound_http (selected framework bindings),
  db_operation (repository calls, raw SQL, and selected ORMs), queue_publish
  (known clients with resolvable destinations), command_exec, outbound_rpc
  (generated gRPC stubs), and stream_consume (selected stream APIs). Coverage is
  intentionally conservative and uneven by language/framework. Cache operations,
  webhooks, CLI commands, RPC endpoints, and connection clients remain primarily
  LLM-owned.
- **LLM sampling is not pinned.** No temperature/seed control yet (provider may
  not honor it on the configured model); residual run-to-run variance is damped
  by the deterministic floor + dedup, not eliminated.
- **Resource-name normalization is best-effort English** (singular/plural). It
  errs toward "don't merge", never a wrong merge.
- **No cost/budget guard.** A run will spend whatever it spends; there is no
  token ceiling or abort-on-exhaustion (deliberately deferred).
- **LLM-owned objectives remain high-variance.** Optional `reask` and `ksample`
  verification can add recall, but June 15 measurements also showed that union
  sampling amplifies noise and cost unless candidate validity is stronger.
- **Evidence validation is still shallow.** A source path and positive line
  number pass the conversion gate; the cited snippet is not yet checked against
  the indexed source range. Plausible-looking negative statements can therefore
  masquerade as evidence.
- **Connection repair is expensive.** On the June 15 scenarios it consumed
  about 138k-237k tokens per run. It restores links the AST misses, but needs
  tighter candidate retrieval and independent precision measurement.

Fixed June 2026: the detail and unused infrastructure stages were removed;
connection repair became visible; deterministic db junk tables are filtered;
`${...}` queue/topic placeholders resolve to real resources; common no-result
sentinels are rejected; and shard fan-out now uses stricter evidence than prompt
hint rendering for confusable objectives.

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
- ✅ **Delete detail + infrastructure** — removes the two LLM stages that did
  not justify their token cost; deterministic conversion/backfills are canonical.
- ✅ **LLM connection repair tail** — additive, fail-soft repair over a closed
  dependency catalog for exposures the AST walk leaves dangling.
- ✅ **Keyword-seeded sharding** — selected LLM-owned objectives can accumulate
  static evidence from imports/calls instead of always using one whole-repo call.

Near-term (the highest-leverage gaps, in priority order):
1. **Full-run labeled stability suite** — label representative real services,
   score every scenario, and add K-run variance metrics. Counts from one run are
   not an accuracy signal.
2. **Evidence-grounded candidate validation** — verify cited path/range/snippet
   against the AST/source snapshot before keep-biased reexamination can retain a
   candidate.
3. **Objective-specific identity schemas** — require the identity-bearing keys
   for each objective in structured output and normalize them before shard,
   verify, and deterministic merges.
4. **Candidate-manifest discovery** — retrieve compact candidate files/symbols
   first, then ask the LLM to classify/enrich that bounded set plus an explicit
   dynamic tail. This is the main recall-per-token improvement.
5. **Verification redesign** — replace whole-repo union sampling with targeted
   missed-candidate checks and an independently measured precision gate.
6. **Extend deterministic coverage** — prioritize external cache operations,
   CLI/RPC/webhook entrypoints, non-Feign HTTP clients, and additional ORM/client
   libraries where precision can be proven.

Medium-term (quality & trust):
7. **Adaptive cost scheduler** — per-objective call/token budgets, shard caps,
   diminishing-return stop rules, and cheaper models for classification.
8. **Connection repair retrieval** — partition dangling exposures and shortlist
   reachable dependency candidates before prompting.
9. **Deterministic-first repo facts and clients** — remove avoidable discovery
   tokens by deriving stack facts and connection backbones from AST/config.
10. **Pin LLM determinism** where supported and report the effective sampling
    configuration in every run.

Longer-term (product):
11. **Cross-service graph** — stitch many services' exposures↔dependencies into
   a fleet-wide architecture graph (e.g. service A's outbound_http → service B's
   http_route). (Placeholder resolution above makes the instance names stable
   enough to stitch on.)
12. **Diff mode** — architecture delta between two commits/versions.

The measured June 15 scenarios and the concrete discovery work plan are in
`docs/DISCOVERY_ROADMAP.md`.

## 7. Where things live

- `internal/objectives/registry.go` — the objective map + prompts.
- `internal/pipeline/` — orchestration, lifecycle, resume, events, and terminal
  result assembly.
- `internal/floor/` — LLM-free deterministic-floor projection used by eval.
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
