# Investigation: enterprise readiness — instance identity, stability, accuracy, token cost

> **Date:** 2026-06-11. **Branch:** `codex/backend-architecture-rewrite` (post
> stage/llmrun/floor refactor). **Reference run:** `~/.diffmind/runs/20260610T105436Z`
> (Spring `routing-service`, sha `fcd84d58`: 46 exposures, 45
> dependencies, 84 connections, 43 LLM calls, 2.34M tokens, ~21 min).
>
> **Relationship to [ACCURACY_ROADMAP.md](ACCURACY_ROADMAP.md):** that document
> is the approved accuracy workplan and findings ledger; this one does NOT
> duplicate it. It re-verifies the state of the system after the architecture
> rewrite, adds the **enterprise framing** (diffmind's output feeds a
> company-wide service graph that matches services by *concrete instance
> identity*), and re-prioritizes existing ledger items against that goal plus
> one finding the ledger does not yet cover (Finding A).

## The downstream contract this report optimizes for

DiffMind's output is consumed by a system that connects every company service
to every other: "service A talks to service B via queue Q" only works if both
sides independently emit **the same identity for Q** — and if two genuinely
different instances never collapse into one. The user-confirmed requirements:

1. **Accuracy first** (consistent with the roadmap's north star) — a wrong edge
   in the company graph is worse than a missing one.
2. **Concrete instance identity** — resolved queue name + broker URL, DB host +
   database name. Logical names alone are not the contract.
3. **Polyglot fleet** — "every language out there"; JVM-only depth is a
   strategic gap, not an acceptable scope.
4. Token/cost optimization, never at accuracy's expense.

## Executive summary

The system is structurally healthy after the rewrite: one canonical pipeline
(`internal/pipeline/pipeline.go`), typed stages under `internal/stage/*` that
never import one another, LLM runtime isolated in `internal/llmrun/`, hermetic
deterministic floor in `internal/floor/`, and `go build`/`go test` green.
Outputs on the reference run are largely correct and grounded (no hallucinated
queues/DBs found; all instances trace to real config).

Three blockers stand between this and enterprise-trustworthy output, in order:

| # | Blocker | Evidence | Status in ledger |
|---|---------|----------|------------------|
| A | **Instance identity is not downstream-consumable** — one physical Postgres DB is emitted as 3 different "instances" in a single run | run `20260610T105436Z`, `dependencies/db_operation.json` | **not in ledger** (new) |
| B | **LLM-only objectives flicker run-to-run** — core/union 0.00 on cli_command (0/13) and outbound_rpc (0/24) vs 1.00 on deterministic types | M4 variance baseline; 3-run drift 55→52→46 exposures | V3a/V3b/S2/D, open |
| C | **Accuracy is unmeasured where it matters** — full P/R/F1 harness exists but one toy fixture; no LLM-inclusive baseline at all | `testdata/eval/` (spring-crud only) | M1/M3, open |

Token spend (~2.34M/run, 65% in discovery) has real reduction levers, and the
best ones — growing the deterministic floor and sharding LLM-only objectives —
*also* fix Blocker B. Cost and accuracy mostly compound here rather than trade
off.

---

## Finding A — Instance identity (the downstream contract) — HIGHEST PRIORITY

### What the reference run actually emits

`db_operation` (34 items in `dependencies/db_operation.json`), all hitting the
**same single PostgreSQL database**, carry three different `instance` values:

| `instance` value | count | problem |
|---|---|---|
| `"spring.datasource.url"` | 30 | the config **key**, not its value — meaningless outside this repo |
| `"unknown"` | 2 | no identity at all |
| `"map[connection_source:… url_template:jdbc:postgresql://${DATABASE_HOST:…}/${DATABASE_NAME:…} …]"` | 2 | a Go map fmt-ed into the string field — a serialization bug, and ironically the only variant that contains the real identity |

A downstream matcher sees **three distinct databases** where one exists. The
map-in-string variant proves the pipeline *has* the right data (connection
source, URL template, auth mode) — it is being destroyed at the boundary, not
missing.

Queues are healthier but still informal:

- `queue_publish` → `instance: "catalog-target-request-sqs"`;
  `queue_consumer` → `instance: "ats-salesforce-data-events-sqs"` etc. Logical
  names resolve correctly through `${...}` placeholders (good), but the broker
  URL / account / region live only in free-text `details`, and `instance`
  duplicates `name` on consumers.
- Cross-service hazard observed in this single repo: `catalog-…-request-sqs`
  vs `catalogue-…-response-sqs` — both spellings are real, but it shows that
  matching on free-text names is fragile; the URL is the durable key.
- `outbound_http` → `instance: "salesforce-account-api"` (logical service name;
  resolved base URLs per environment exist only in details).

### Why this is cheap to fix correctly

The resolution machinery already exists and is deterministic:
`internal/stage/discovery/config_resolve.go` (`ResolvePlaceholder`,
`ResolveResourceName`) resolves `${a.b.c}` and `${a.b.c:default}` against the
parsed config index, follows up to 5 indirections, and never loses data. It is
applied to queue *names* today but not to **instance identity** for databases,
brokers, or HTTP targets — and there is no structured schema to put the result
in (`model.BaseEntity.Instance` is a bare string, `internal/model/types.go`).

### Recommendation (the one genuinely new roadmap item)

Define a structured, cross-service-matchable instance contract and make it the
emitted artifact (additive — keep the string `Instance` for compatibility):

```jsonc
"instance": {
  "kind": "postgres" | "sqs" | "kafka" | "http" | ...,
  "logical_name": "catalog-target-request-sqs",   // resolved ${...} name
  "url_template": "jdbc:postgresql://${DATABASE_HOST:localhost}:${DATABASE_PORT:5432}/${DATABASE_NAME:routing_db}",
  "resolved": { "host": null, "database": "routing_db", ... }, // best-effort; nulls where env-only
  "config_source": "application.yml: spring.datasource.url",
  "per_environment": { "prod": "...", "stage": "..." }                   // when profiles define them
}
```

Rules: emit **both** the logical name and the best-effort resolved value;
preserve env-var templates verbatim when unresolvable (the downstream system
can match `${DATABASE_NAME:routing_db}` defaults or join on its own
env data); **never** emit a config key or a fmt-ed Go struct as identity. The
dedup/identity layer (`internal/entitykey/entitykey.go`) should gain an
instance component only once the schema exists, so distinct real instances
stop collapsing and identical ones stop splitting.

This also widens the detail-stage deterministic fast path (complete seeds skip
LLM detail entirely) — an accuracy fix that *removes* tokens.

---

## Finding B — Run-to-run stability (graph edges must not flicker)

The company graph is rebuilt from repeated runs; an edge that appears in run N
and vanishes in run N+1 destroys downstream trust faster than a uniformly
missing edge.

**Measured** (M4 variance harness, baseline in ACCURACY_ROADMAP §M4):
deterministic types are perfectly stable (core/union **1.00**); LLM-only types
are not — **cli_command 0/13, outbound_rpc 0/24, queue_consumer 0/6** (the last
traced to V3a config nondeterminism, since fixed at the consumer-name level but
not at the root).

**Re-confirmed on the three latest runs** of the same repo
(`routing-service`):

| run | exposures | dependencies | connections | tokens | LLM calls |
|---|---|---|---|---|---|
| `20260609T205100Z` | 55 | 52 | 113 | 2.28M | 46 |
| `20260609T225441Z` | 52 | 47 | 99 | 2.44M | 50 |
| `20260610T105436Z` | 46 | 45 | 84 | 2.34M | 43 |

Caveat: the two earlier manifests predate the git-SHA field, so source drift
between runs can't be fully excluded — which is itself the ledger's
"add git SHA to manifest" point, now shipped (`repo_git_sha` present in the
latest manifest). The variance harness numbers above are SHA-independent.

**Root causes are already identified in the ledger; this report only
re-prioritizes them as enterprise blockers:**

- **V3a (HIGH, open, fix-rule approved):** `config_resolve.go` iterates a Go
  map, so when `application.yml` and a profile override define the same key,
  the winner is random per run → unstable resource names → unstable identity
  keys → flickering edges. The approved rule (base authoritative unless profile
  override + unknown active profile → unresolved, retain candidates) is exactly
  what Finding A's `per_environment` field needs anyway — fix once, serve both.
- **V3b (HIGH, open):** YAML list-of-mappings mis-keyed; real YAML decoder
  closes V3b/c/e together.
- **S2 (MED, open):** detector-less objectives never shard, so each gets one
  whole-repo "find ALL X" call — the highest-variance prompt shape and the
  worst truncation risk. Keyword seeding (reusing `grounding.go` matcher lists)
  makes them shard like everything else.
- **D (sequenced last, correctly):** sampling controls via a selected OpenCode
  agent; judge by M4 only after V3/S land, so it measures residual variance.

---

## Finding C — Accuracy is unmeasured where it matters

The harness is genuinely good — `internal/eval/` has cheap (hermetic floor,
CI-gated at F1 = 1.0), score-run, variance (M4, DONE), and floor-coverage (M5,
DONE) modes, all keyed by the same identity functions the pipeline's dedup uses
(`eval/identity.go` → `entitykey.SemanticLoose`), so "correct" and "duplicate"
are one definition. But it is starved:

- **One fixture** (`testdata/eval/spring-crud`): 2 routes, 2 db ops, 2
  connections, JVM, deterministic-only. Every precision/recall number for the
  LLM-inclusive pipeline — i.e., for what actually ships — is **unknown**.
- **No connection-level grading** has ever happened (the machinery exists;
  labels don't). Ledger A1: 13/46 exposures (8 routes + 5 cron) have **zero
  connections** because connections are 100% deterministic AST with no LLM
  repair tail — for a project whose entire purpose is edges, a ~28% dangling
  rate is the single largest known recall gap.
- **No instance-identity grading exists at all** — and per Finding A it is the
  downstream contract. Labels (`internal/eval/label.go`) support `instance`,
  but no fixture exercises it.
- Production gates (P ≥ 0.95/R ≥ 0.90 floored, P ≥ 0.85/R ≥ 0.75 LLM-only,
  connection P ≥ 0.85/R ≥ 0.80, core/union ≥ 0.95) are structurally approved
  but numerically uncalibrated — they need M3's labeled fixtures to mean
  anything.

**Re-prioritization for the enterprise goal:** M3 fixtures must include (a) at
least one fixture per major fleet language, (b) connection labels, and (c)
instance-identity labels — a fixture pair where two "services" share a queue,
graded on whether both emit the same instance key. M1 (label assist) stays the
cost-of-labeling enabler.

---

## Finding D — Multi-language gap vs. the "every language" goal

Floor coverage today (verified against `internal/ast/framework/` and
`internal/stage/discovery/deterministic.go`):

| capability | JVM/Spring | Node/TS | Python | Go | others |
|---|---|---|---|---|---|
| http_route | strong | partial (Express, NestJS) | partial (Flask, FastAPI) | partial (net/http, gin, echo) | — |
| queue_consumer / scheduled_job | strong (@KafkaListener/@SqsListener/@Scheduled…) | — | — | — | — |
| db_operation | strong (Spring Data/JPA/MyBatis) | **none** | **none** | **none** | **none** |
| queue_publish / outbound_rpc / command_exec | partial (call-graph) | — | — | — | — |

On a non-JVM repo, the facts the downstream graph needs most — DB and queue
dependencies — fall **entirely** to the LLM tail, which Finding B shows is the
unstable part. So the polyglot goal makes ledger **F6** (non-JVM db_operation:
GORM, Django ORM, Sequelize/Prisma/TypeORM, sqlx + multi-language routes:
Go stdlib, Django urls.py, JAX-RS) the highest-leverage detector work, with
**F5** (gRPC server, needs parser support) behind it. Each detector added:

1. raises precision and recall on that stack (floored facts, confidence 1.0),
2. stabilizes it (deterministic ⇒ core/union 1.00),
3. cuts tokens (high floor coverage ⇒ smaller/skipped LLM discovery; measured
   by the existing `eval --mode floor-coverage`),
4. feeds AST connections (the BFS can only walk edges the AST knows).

Sequencing detectors **by actual fleet composition** (which languages dominate
the services that will be scanned first) is the right ordering input; the
existing detector pattern in `internal/ast/framework/{spring,web,nestjs}.go`
is the template, and every new detector ships with an M3 fixture so cheap-mode
CI ratchets it permanently.

---

## Finding E — Token/cost breakdown and levers

From the reference run's `run_manifest.json` token accounting
(`internal/llmrun/tokens.go` populates it per stage):

| stage | calls | input | output | reasoning | cache read | total |
|---|---|---|---|---|---|---|
| discovery | 33 | 1,382,316 | 55,723 | 84,376 | 2,160,128 | **1,522,415 (65%)** |
| detail | 8 | 609,428 | 48,816 | 61,675 | 1,452,032 | **719,919 (31%)** |
| repo_facts | 1 | 68,119 | 2,471 | 3,837 | 58,880 | 74,427 |
| other | 1 | 15,334 | 1,706 | 4,838 | 0 | 21,878 |
| **total** | **43** | 2,075,197 | 108,716 | 154,726 | 3,671,040 | **2,338,639** |

Correction to a roadmap-era assumption: **provider-side prompt caching is
already working** — 3.67M cache-read tokens (≈ 64% of what input would
otherwise be). The X-workstream's "measure, don't build breakpoints" stance is
confirmed correct; no client-side cache work is warranted.

Levers, ranked by (savings × accuracy-safety):

1. **Floor coverage as the cost dial** (compounds with Finding D). Discovery is
   65% of spend and is dominated by per-objective enumeration calls. Every
   objective the floor covers well can have its LLM discovery shrunk to a
   "find what the floor missed" residual — `eval --mode floor-coverage`
   already measures exactly this per objective, per run. This is the only
   lever that *increases* accuracy while cutting cost.
2. **Shard LLM-only objectives (S2)** — same fix as the variance item: today
   they get whole-repo scans (cost + variance + truncation in one). S3's
   observed waste (rpc_endpoint fanned into 11 shards, 2.7× calls) shows
   fan-out tuning belongs in the same change, validated via M before shipping.
3. **Per-shard context duplication** — each shard re-carries REPO_FACTS +
   AST_HINTS + framework patterns (~1–3K tokens × shard count;
   `internal/extraction/prompts.go:60-119,296-331`). Shard-scoped hints exist
   (`Shard.Hints`); trimming repo-wide blocks from shard prompts is a bounded,
   provably-safe cut. Low single-digit % of discovery spend — worth taking,
   not worth leading with.
4. **Detail fast-path widening** — deterministic-complete seeds already skip
   detail entirely; Finding A's structured instance resolution makes more
   seeds complete, shrinking the 31% detail share as a side effect.

Not recommended: cutting `KNOWN_CONFIRMED_ITEMS` or AST_HINTS caps to save
tokens — both exist to prevent re-discovery and grounding loss; per the
roadmap's own rule, accuracy wins that tradeoff and the cost is visible in the
manifest.

### Minor manifest hygiene (observed, low priority)

`stage_failures: {"other": 3}` appears in all three recent runs with no
corresponding `stage_failed` events in `events.jsonl`, and `cost` is always 0
while token counts are real. Neither affects output correctness; both make the
manifest less trustworthy as an ops artifact and are worth a small cleanup
when touched next.

---

## Prioritized roadmap (sequenced for the enterprise goal)

Ordering principle unchanged from ACCURACY_ROADMAP ("accuracy wins; measure
everything through M"); what's new is the instance-identity contract at the
front and fleet-driven ordering of F6.

1. **Instance identity contract + resolution** *(new — promote into the
   ledger)*. Structured instance schema on `model.BaseEntity`; apply
   `config_resolve.go` resolution to DB/broker/HTTP instances; fix the
   map-in-string serialization bug; emit logical name + template + best-effort
   resolved value + config source. Unblocks the downstream graph; everything
   else polishes what this makes consumable.
2. **Stability: V3a (rule already approved) + V3b YAML decoder + S2 sharding
   of detector-less objectives.** Kills edge flicker at its measured roots;
   S2 also cuts discovery tokens. Verify with `eval --mode variance` (target
   core/union ≥ 0.95 on currently-failing objectives).
3. **Measurement: M1 label assist, then M3 fixtures** — per fleet language,
   with connection labels and **instance-identity labels** (shared-queue
   fixture pair). Calibrate and freeze the production gates. From this point
   every later item is judged by numbers, not narrative.
4. **Multi-language floor: F6 first (non-JVM db_operation + routes), ordered
   by fleet composition; F5 (gRPC server) behind it.** Accuracy, stability,
   and token wins compound (Finding D). Each detector lands with a fixture.
5. **A1 connection repair tail** (zero-connection exposures only, additive,
   `Source∈{ast,llm_repair}` provenance per A1-schema) — closes the ~28%
   dangling-exposure gap that directly hides graph edges.
6. **Precision tail:** P1 (operation_kind enum), P3/P3b (objective-boundary
   disambiguation + sqs_consumer alias fix), E3 (finder-method ops), then
   S1/S3 sharding quality and D (sampling agent), all gated by the calibrated
   M3 numbers.

## What was verified, and how

- Pipeline structure, stage wiring, prompt builders, sharding, token
  accounting: read on branch `codex/backend-architecture-rewrite`
  (`pipeline.go`, `stage/discovery/{sharding,grounding,deterministic,config_resolve}.go`,
  `extraction/prompts.go`, `llmrun/tokens.go`, `floor/floor.go`,
  `stage/reconcile/policies.go`, `entitykey/entitykey.go`, `eval/*`).
- All run numbers: `~/.diffmind/runs/{20260609T205100Z,20260609T225441Z,20260610T105436Z}/run_manifest.json`
  and `dependencies/*.json`, `exposures/*.json` of the latest run (instance
  values quoted verbatim from `db_operation.json`, `queue_publish.json`,
  `outbound_http.json`, `queue_consumer.json`).
- Variance figures: ACCURACY_ROADMAP §M4 baseline (run `20260608T230315Z`).
- Ledger IDs and statuses: ACCURACY_ROADMAP findings ledger as of this branch
  (E1/E2/C1–C5/F1–F4/M4/M5 DONE; E3/P1–P3/A1/V3a/V3b/F5/F6/S1–S3 open).
