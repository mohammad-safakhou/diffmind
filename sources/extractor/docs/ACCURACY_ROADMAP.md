# DiffMind — accuracy-first roadmap to a concrete working extractor

## Goal & scope (the north star)

Build a **perfectly functioning** architecture extractor (exposures,
dependencies, connections) across many languages/frameworks. Priorities, in
order:

1. **Accuracy** — maximize recall + precision of extracted facts. On any
   tradeoff, accuracy wins.
2. **Cost** — minimize LLM tokens/calls, but never by sacrificing accuracy.

**Explicitly out of scope for now** (deferred, not forgotten): security/trust
boundary (symlink escape, webfetch exfil, prompt-injection hardening), and
enterprise-hardening reliability that doesn't affect accuracy or cost
(checkpoint version fingerprinting, file-size/OOM guards, cross-service graph,
diff mode). These are listed at the end so they aren't lost.

> **The keystone:** you cannot improve accuracy you cannot measure. Today
> accuracy is measured on ONE toy fixture in deterministic-only mode. So
> **Workstream M (measurement) is the foundation and ships first** — every other
> workstream is validated through it.

All findings below were verified against source (file:line) and the real run
`~/.diffmind/runs/20260608T230315Z` (Spring `routing-service`: 45
exposures, 59 deps, 92 connections, 47 LLM calls, 1.47M input / 126K output
tokens, 21 min).

## Invariants to preserve (from CLAUDE.md) + one new rule

One canonical pipeline; language-scoped prompts; evidence-gated sharding;
detail is additive; dedup targets the architectural fact; deterministic facts
are high-precision (emit nothing over a guess). **New rule:** when accuracy and
cost conflict, choose accuracy; record the cost in the eval report so the
tradeoff is visible.

---

# Workstream M — Measurement (the foundation; build FIRST)

Today: `internal/eval/` is well-built — `identityKey` (`identity.go:18`) already
keys **every** objective type and connections (`score.go:186-214`), cheap mode
**does** score connections, and matching reuses `reconcile.SemanticKeyLoose` so
"correct" == "duplicate". The gap is **not** matcher code — it's fixtures,
labels, and missing measurement modes.

### M1 — `diffmind label` assist (new `cmd/diffmind/label.go`): make labeling near-free
- **`label init --run <id> --out testdata/eval/<name>`** — project a finished
  run's artifacts (reuse `eval.LoadRunArtifacts`, `artifacts.go:17`) down to
  label-shape (only the fields `identityKey` consumes), pre-marking
  `deterministic:true` for items carrying the deterministic tag and
  `verified:"unconfirmed"`. Labeling becomes "delete wrong rows, flip a flag"
  (~10 min for a 100-item real run) instead of authoring JSON.
- **`label diff --run <id> --fixture <dir>`** — score in `ModeFull`, print FP/FN
  lists (`report.go:24`); human reconciles only the deltas. This is the
  steady-state loop after every pipeline change.
- **`label promote`** — for the floor's high-precision types (http_route, JVM
  db_operation), auto-accept floor output as ground truth (`verified:"floor-oracle"`),
  so humans only hand-label the LLM-only types (queue_publish, cache_operation,
  outbound_http, connections). The deterministic floor *is* a partial oracle per
  invariant #6.

### M2 — Label format extensions (`internal/eval/label.go`)
Add `Language string` (the one genuinely missing field — enables per-language
P/R/F1; never enters `identityKey`, costs nothing in matching) and
`Verified string` ("human" | "floor-oracle" | "unconfirmed") for honest
provenance. Keep line-numbers/details OUT (the matcher is intentionally
path/line-insensitive; pinning them adds cost + brittleness). Add fixture-level
`languages:[...]` for a sanity check against detected `repo_facts`.

### M3 — Real multi-language fixtures (`testdata/eval/`)
4–6 small-but-real repos, one per target language (Spring; Go net/http or gRPC;
Python FastAPI/Celery; Node/Express), plus the no-code JVM quick wins
(`messaging-jvm` for queue_consumer+scheduled_job; `connections-jvm`). Build via
M1's loop. Flag an item `deterministic:true` only once the floor actually
recovers it, so the cheap-mode F1=1.0 gate ratchets honestly.

### M4 — Variance harness (new `internal/eval/variance.go`)
K-run stability: bucket each run's entities by type, build `SemanticKeyLoose`
key-sets, report per-objective count mean/stdev and the **core/union ratio**
(keys in ALL K runs ÷ keys in ANY run — 1.0 = perfectly reproducible). CLI:
`eval --mode variance --runs id1,id2,...` (scores finished runs, no OpenCode) +
a `run --k N` convenience. This is the *proof tool* for every stability claim
(sharding, prompt enums, temperature) — measure before/after.

### M5 — Floor-recall metric (new `internal/eval/floor_recall.go`)
`--mode floor-recall`: run `DeterministicFloor` and load the full LLM run for
the same repo; per type compute `|floor_keys ∩ llm_keys| / |llm_keys|`. This is
**the cost-reduction lever's dial** — it tells you what fraction of each type the
free pass already covers, so you know where pushing to deterministic (Workstream
F) cuts LLM cost without losing accuracy.

### M6 — Metrics & gating
- Per-objective + **per-language** P/R/F1 (bucket `ObjectiveScore` by the new
  `Language` tag), connection P/R, floor-recall %, and **run cost** (fold
  `run_manifest.token_totals`) reported alongside accuracy, so every accuracy
  gain shows its token price.
- Gating layers: (a) **cheap-mode** `TestCheapAccuracyFloor` (`cheap_test.go`,
  extend the `map[fixture]minF1`) — hermetic CI, have it; (b) **full-mode
  record-replay** — a cassette layer hashing OpenCode req→resp under
  `testdata/eval/<fixture>/cassette/`, replayed in a new `full_replay_test.go`
  to gate LLM-driven types hermetically and deterministically; (c) **nightly
  `make accuracy`** (needs live OpenCode) for score-run + variance + floor-recall
  trends into `testdata/eval/history/`. *Caveat: the cassette layer is the
  biggest new infra; scope how cleanly the OpenCode client is interface-abstracted
  before committing.*

---

# Workstream P — Prompts & output contract (highest accuracy/variance-per-token)

The dominant variance + quality problem: the output schema treats `details{}` as
free text (`schemas.go:16` `additionalProperties:true`) and prompts teach values
by conflicting prose. One run produced **13 spellings** of `db_operation.operation`
for ~3 real classes. These are cheap prompt/schema edits with the biggest
accuracy+stability payoff.

### P1 — Closed enums in the per-objective schema (not just prose)
In `entitySchemaForObjective` (`classify.go:59`) inject per-objective `details`
sub-schemas with `enum`s (OpenCode validates server-side, `schemas.go:3`):
`db_operation.operation ∈ {read,write}`; `cache_operation.operation ∈
{read,write,evict,expire}`; `platform` lowercased enum `{sqs,sns,kafka,...}`;
`http method ∈ {GET,POST,...}`. Replace the conflicting prose at
`registry.go:253,260` with one canonical mapping line ("SELECT/find/get→read;
INSERT/UPDATE/DELETE/save→write; if a method does both, emit TWO items; never
prose, never 'unknown'"). The schema enum is what actually *enforces*; the prose
alone drifts.

### P2 — Fix the `queue_publish` key contract
Example/DetailKeys teach `queue` (`registry.go:473`) but the reexamine gate
requires `destination` (`reexamine.go:73`); it only passes via a fragile alias
(`reexamine.go:334`). Pick ONE canonical key (`destination`), make example +
DetailKeys + gate agree, state it explicitly. Audit all of `reexamine.go:62-77`
required keys against the registry examples for the same mismatch.

### P3 — `OBJECTIVE_BOUNDARIES` disambiguation block
Discovery objectives run in parallel, each blind to its siblings → double-claims
and gaps. Add a shared 1-line-per-sibling boundary block to `buildDiscoveryPrompt`:
- **http_route vs webhook:** webhook = only signature-verified/provider-callback
  endpoints; a plain authenticated POST is http_route.
- **outbound_http vs aws-sdk:** outbound_http EXCLUDES AWS SDK calls to
  SQS/SNS/DynamoDB/Kinesis/Athena/S3 (those are queue/db/stream objectives) —
  the run already needed `dropTransportDuplicates` (`reconcile.go:349`) to undo
  this.
- **db_operation vs cache_operation (Redis):** both currently claim Redis
  (`registry.go:255` vs `:367`). Assign Redis-as-cache→cache_operation,
  Redis-as-datastore→db_operation, explicitly in both.
- **queue_consumer vs stream_consume:** dedupe Kinesis overlap. **Fix the likely
  wrong alias** `"sqs_consumer":"stream_consume"` (`classify.go:24`) — an SQS
  consumer is a queue_consumer exposure, not a stream dependency.

### P4 — Stop noisy/misleading hints
`command_exec` (no real candidates) was still fanned into 4 shards over generic
dirs, each shipping 40 irrelevant `CANDIDATE_SYMBOLS` (getters/predicates) — pure
token cost that primes false positives. Suppress AST hints for objectives whose
candidates are only generic symbols (no objective-specific annotation/binding);
collapse zero-binding objectives to a single whole-repo call. (Ties to Workstream
S sharding.)

### P5 — Scope framework bullets to known frameworks, not just languages
`scopeFrameworkPatterns` (`prompts.go:279`) strips absent *languages* but keeps
all *framework* bullets — a Spring repo's db_operation prompt still ships
DynamoDB/Elasticsearch/Mongo patterns (`registry.go:233-237`) × 5 shards × every
run, bloating tokens and priming false positives. Also scope framework bullets
against `repo_facts.frameworks`/`probable_tech_hints` when confidently known
(keep unknown→keep-all).

### P6 — Empty-return + anti-truncation guidance on the high-volume objectives
Add explicit `{"items":[]}` guidance to the big five (http_route, queue_consumer,
db_operation, outbound_http, queue_publish — currently missing it) and a "if
>~50 items, prioritize by confidence, never return partially-cut JSON" rule;
log when parsed `items` look truncated.

### P7 — Reconcile backstop for canonicalization (defense in depth)
Even with P1 enums, keep a deterministic backstop in `reconcile`: canonicalize
the emitted `details.operation` to read/write while preserving `raw_operation`;
resolve name/operation contradictions (single source of truth); derive
`outbound_http.service` from the Feign client name or URL host (all 10 were
null); infer specific `platform` from driver/datasource hints instead of generic
`"database"`. Eval-safe (idempotent with `SemanticKeyLoose`); add a consistency
test. *(This is the original "output normalization" work, now backed by P1 at the
source.)*

---

# Workstream C — Correctness bugs that corrupt accuracy/measurement

Fix these early — they create duplicates and data loss that would poison the
eval signal.

- **C1 — HTTP path params never canonicalized (duplicates + eval FP/FN).** No
  code maps `{id}`↔`:id`↔`<int:id>`↔`*`; identity uses verbatim path
  (`deterministic_discovery.go:590`, `eval/identity.go:90`). Add a shared
  `canonicalizeRoutePath` applied in both identity functions. *(Base-path concat
  is already correct — `joinPath`, `spring.go:176`.)*
- **C2 — Detail can re-identify an entity (violates invariant #4).**
  `mergeEnrichment` (`detail.go:615,635`) lets the LLM's enriched Name/details
  win; ID derives from Name (`convert.go:73`), so a rewritten name → new
  identity → broken dedup/connection binding. Pin Name + identity details to the
  seed for discovered/deterministic entities; regression-test `out.Name ==
  seed.Name`.
- **C3 — Reexamination deletes true positives (data loss).** One LLM "no" drops
  a real low-confidence item (`reexamine.go:509,583`). Require corroboration
  before deletion, or downgrade-don't-drop; never let one conservative "no" erase
  discovered architecture. (Note: in the audited run reexamination made 0 calls —
  `deriveDetailsFromName` cleared all suspects — so this is latent, fires on
  noisier repos.)
- **C4 — Schema-qualified resource false-split (duplicate).** `public.orders` vs
  deterministic `orders` → different keys (`reconcile.go:303`). Strip schema
  qualifier + unify snake/camel before keying.
- **C5 — `delete` folded into `write` (decide).** `normalizeDBOp` (`reconcile.go:333`),
  `inferDBOperationKind*` (`connections.go:784,1243`) collapse delete+insert+update
  → write, so DELETE-orders and INSERT-orders merge. Decide explicitly: keep a
  `delete` class (more accurate) or document the fold. Accuracy-first leans toward
  keeping `delete`.

---

# Workstream E — Deterministic extraction correctness (fix the floor before growing it)

A wrong deterministic fact is merged into output AND injected into the LLM as
"KNOWN_CONFIRMED_ITEMS — do not rediscover," so a precision bug here is the worst
kind: it corrupts output and misleads the LLM. These are verified (probe-tested),
high-frequency, and directly violate invariant #6. **Fix before expanding the
floor (F).** Note: `internal/ast/framework/` currently has **zero test files** —
every fix lands table tests there.

- **E1 — Spring route fabrication from `produces`/`consumes`/`headers`/`params`
  (CRITICAL, accuracy).** `extractStringArgs` (`spring.go:166,240`) treats *every*
  string literal in the annotation as a route path. `@PostMapping(value="/orders",
  produces="application/json")` → emits `POST /orders` **and** a phantom
  `POST /application/json`; `@GetMapping(produces=...)` with class-level
  `@RequestMapping("/orders")` → emits `GET /application/json` instead of the real
  path. Confidence 1.0, very common in real controllers. **Fix:** take the path
  only from the positional first arg or the `value=`/`path=` attribute; never from
  `produces/consumes/headers/params/name`. (Array form `{"/a","/b"}` is already
  correct.) Highest-priority fix in the whole plan.
- **E2 — Kafka/Rabbit array-topic mangling (HIGH, accuracy).** `extractArgValue`
  (`spring.go:86,101,225`) reads "until first comma" and can't handle
  `topics={"orders","shipments"}` → emits queue name `{"orders` and silently drops
  `shipments`. **Fix:** parse `{...}` arrays (reuse the route array logic); emit
  one consumer per topic.
- **E3 — Finder-method op misclassification (MEDIUM, accuracy, bounded).**
  `looksLikeFinderMethod` (`connections.go:1268`) prefixes `all`/`scan`/`page`/
  `load`/`fetch`/`stream` flag genuine writes as `read` (`scanAndPurge`,
  `pageThroughAndDelete`, `fetchAndRemove`). Table stays correct; only read/write
  flips. **Fix:** tighten the prefix set; keep the `@Modifying`/`@Query` checks
  (which already run first and are correct).
- **E4 — `@Scheduled(cron=..., zone=...)` schedule field (LOW).** Whole arg string
  stored as `schedule` (`spring.go:57`, `deterministic_discovery.go:389`). Doesn't
  affect identity (name = handler symbol), just an ugly field. **Fix:** parse the
  `cron=`/`fixedRate=` attribute.
- **E5 — `extractFirstStringArg` returns raw arg when no quoted string (LOW).**
  Low practical impact (destinations are normally quoted); note and guard.

# Workstream F — Deterministic "AST-before-LLM" floor for every stage + multi-language

This is simultaneously the **biggest accuracy-stabilizer** (deterministic types
don't swing run-to-run) and the **biggest cost lever** (work the floor recovers
is work the LLM needn't, tracked by M5 floor-recall). Each detector ships only
behind invariant #6 (emit nothing over a guess), paired with an M3 fixture whose
flag flips in the same commit.

- **F1 — cache_operation wiring (near-free).** Spring already emits
  `@Cacheable/@CachePut/@CacheEvict` bindings; they're dropped because only
  http/feign/queue_consumer/scheduler are wired into `objectiveForBinding`
  (`deterministic_discovery.go:252`) + `supportedDeterministicObjectives`
  (`:233`). Wire cache through. Smallest change, immediate floor-recall gain.
- **F2 — command_exec** (`Runtime.exec`/`ProcessBuilder`/`os/exec.Command`/
  `subprocess`), **F3 — queue_publish** (`SqsTemplate/KafkaTemplate.send`,
  `boto3 publish`; resolve destination via `config_resolve.go`, emit the publish
  fact even when the name is uncertain), **F4 — gRPC** outbound_rpc/rpc_endpoint
  (stub/ImplBase types). Each call-site based, behind the precision bar.
- **F5 — multi-language DB derivers** (the JVM-only floor is the documented worst
  case for non-JVM). Ranked: **GORM** (Go) → **Django ORM** (Python). Match verb
  calls, resolve table/model, emit nothing if unresolvable. `isRepositoryOperationSymbol`
  (`connections.go:737`) is JVM-convention-locked today.
- **F6 — multi-language route detectors** (highest-impact missing): **Go stdlib
  `net/http` ServeMux** (a plain Go service gets zero deterministic routes
  today), **Django `urls.py` urlpatterns** (the detector parses signals/Celery
  but NOT Django's actual routes — `web.go:128`), **Flask** and **JAX-RS** (both
  in prompts, no detector). Express receiver-name brittleness (`web.go:41`).

---

# Workstream A — Connections (close the 40% dangling gap)

18/45 exposures got zero connections — all queue_consumers, all scheduled_jobs,
9 http_routes — because the AST BFS (`connections.go:298`) can't cross
DI/interface, async/event, or message/cron boundaries.

- **A1 — LLM connection repair tail (Stage 4.5)**, additive, after the
  deterministic walk, only for exposures with zero connections. The LLM picks
  targets from a **closed set** of existing dependency IDs (hallucinations
  rejected, then orphan-dropped by the existing `reconcile.FilterConnections`).
  Tagged `llm_repair`, confidence-clamped below deterministic, checkpointed,
  fail-soft. New `internal/agents/connection_repair.go`; integrate between Stage
  4 and reconcile (`pipeline.go:~800`). *(Now safe to measure because C1/C2 fix
  identity first.)*
- **A2 — Surface walk truncation (accuracy honesty).** `scip/walker.go:190-206`
  silently bails at MaxPaths/MaxDepth/MaxVisitedEdges — "no connection" is
  indistinguishable from "capped." Set a per-walk `truncated` flag → run warning
  (grounding hints already do this; mirror it).

---

# Workstream S — Sharding (make it meaningful + tune for cost)

Today `planDiscoveryShards` (`sharding.go:83`) sorts candidate files
**alphabetically by path** and greedy-packs to weight 40 — splitting coherent
modules and bundling unrelated dirs. It also fanned `rpc_endpoint` into **11
shards** (2.7× the call count of one-per-objective).

- **S1 — Cohesion clustering** (replace alphabetical packing): cluster candidate
  files by nearest module/feature root, reinforced by **call-graph connectivity**
  (`idx.CallGraph` union-find) so a controller and the service it calls share a
  shard; pack whole clusters; only split a cluster that alone exceeds the cap.
  A shard becomes "this feature + the code it calls."
- **S2 — Keyword seeding for detector-less objectives** (so they shard at all,
  where F doesn't cover them): add `clientLibs` to `objectiveMatcher`
  (`grounding.go:72`); weight imports (+2), client-lib call receivers (+1),
  literal args; merge into the candidate map. No raw file grep — the AST index
  already holds imports/calls/literals.
- **S3 — Tune shard fan-out for cost** (validate via M before shipping): raise
  `discoveryShardSoftTarget` toward 60 and/or cap shards-per-objective (~6) to
  cut the 2.7× multiplier. Measure rpc_endpoint recall at 40 vs 60 on the eval
  set first — **don't change blind**.

---

# Workstream X — Cost levers (accuracy-neutral; cost is #2)

- **X1 — Cross-call prompt/session caching (biggest cost win).** Today
  `ReuseOpenCodeSession=false` (`config.go:110`) → 48 sessions for 48 calls;
  the 6.6M cache_read is within-call only. The same build/config/base files are
  re-read cold across 35 discovery + 10 detail calls (input is 67% of cost and is
  mostly repeated file reads). Enable cross-call caching — but **worker-safe**:
  the shared session is unsafe with `Workers>1` (`pipeline.go:1349`, one
  `AbortSession` kills all in-flight), so use **one shared session per worker**
  or an explicit provider cache breakpoint on the stable repo context, not a
  single global session.
- **X2 — Reorder prompts stable-first** (`prompts.go:327`): today volatile
  content (objective/scope/hints) is first, so the cacheable common prefix is
  only ~250 tokens. Put readOnlyPreamble + HARD RULES + objective-static text
  first, volatile scope/hints/seed last. Multiplies X1 at zero accuracy cost.
- **X3 — Global token/time budget + wire-or-delete `MaxCatalogItems`.** The knob
  is dead config (`config.go:24`, never consumed) yet the failure tip cites it
  (`persist.go:549`). Either make it truncate the catalog AND add a global
  token/wall-clock budget that aborts cleanly, or delete the knob+tip. Prevents
  monorepo blowups (no global ceiling exists today).
- **X4 — Per-stage model tiering (feature ask).** repo_facts + reexamine could
  use a cheaper/faster model; keep the strong model for discovery + detail.
  Needs a per-stage model field (`config.go` has only one `ModelID`). Validate
  no accuracy loss via M before adopting.
- **X5 — Cross-objective detail batching** for tiny same-`kind` leftovers
  (`grouping.go:24`) — minor; only matters on sparse-objective monorepos.
- **X6 — Dead infrastructure-stage LLM call (free cost cut).**
  `runInfrastructureStage` (`ast_stage.go:154`) sends a full config dump to the
  LLM and persists `infrastructure.json`, but the result is discarded:
  `_ = infra // used later...` (`pipeline.go:509`), with no other reader. Pure
  wasted tokens on every run that has config files. **Fix:** either consume it
  (wire resolved endpoints into the detail stage — the original intent) or skip
  the call until it's wired. Decide based on whether it would improve accuracy;
  if yes, wire it; if not, delete it.

---

# Workstream D — Determinism / sampling controls (measured, not assumed)

Add optional low `temperature`/`seed`/`top_p` to the OpenCode request body
(`client.go:404,491` send none today). **Caveat:** OpenCode is a proxy — verify
it forwards them and the provider honors `seed` before claiming determinism.
Sequence LAST and judge purely by the M4 variance harness (core/union ratio
before/after), so it measures residual variance after F/P/S rather than masking
whether those were the real stabilizers.

---

# Workstream V — Coverage expansion (demand-driven, after the core works)

Whole categories are invisible or mislabeled today (`registry.go:36-420`):
- **V1 exposures:** GraphQL resolvers/mutations/subscriptions; WebSocket/STOMP/
  SSE; gRPC streaming semantics; **serverless event-source triggers** (S3/
  DynamoDB-stream/EventBridge/API-GW Lambda are misfiled as `cli_command`).
- **V2 dependencies:** object storage (S3/GCS), secret stores (Vault/SSM),
  feature flags, email/SMS/push, payment SDKs, file I/O.
- **V3 config resolution + the hand-rolled YAML parser** — directly affects
  resource-name accuracy → dedup keys, and is a **non-LLM variance source**:
  - **V3a — Nondeterministic cross-file resolution (HIGH, variance).**
    `configValue` (`config_resolve.go:90`) iterates `idx.Configs` (a Go map →
    randomized order); when `application.yml` and `application-prod.yml` (or
    `values.yaml` overriding base) define the same key, the winner varies
    run-to-run → unstable resource names/dedup keys *with the LLM held constant*.
    Temperature/seed (Workstream D) can't fix this. **Fix:** deterministic
    profile/helm-aware file precedence.
  - **V3b — YAML list-of-mappings mis-keyed (HIGH, accuracy).**
    `parseYAMLEntries` (`parser.go:994,1022`) doesn't open list-item scopes; a
    sequence of mappings collapses all items to one key (`listeners.queue` for
    every listener, `name` lost). Common in Spring Cloud Stream / helm. **Fix:**
    use a real YAML decoder (closes V3b, V3c, V3e together).
  - **V3c — YAML scalar lists silently dropped (MEDIUM).** `queues.names: [a,b]`
    → zero entries → `${...}` falls back to a guessed segment.
  - **V3d — Test-resource configs pollute the index (MEDIUM).** Config files are
    NOT filtered by `isTestLikePath` (`index.go:51,55`), so
    `src/test/resources/application.yml` (deliberately fake local values) feeds
    resolution + the infra prompt. **Fix:** apply the test-path filter to config
    files too.
  - **V3e — Mis-indented YAML re-parented (LOW).** Malformed input → confident
    wrong key, no error.
  - Plus the originally noted gaps: env vars (`getenv`/`os.environ` — extremely
    common for K8s queue/db names), multi-placeholder `${a}${b}`, SpEL.
  - *(Verified CORRECT, no action: polyglot language scoping keeps all present
    languages; language-alias handling (`canonicalLanguage`) and `langdetect`
    are robust and fail-safe; `.properties`/`.env` dotted-key parsing and
    config_resolve's never-lose-data fallback chain are sound.)*
- **V4 auth/authz** (lower priority — metadata, not a core exposure/dep):
  deterministic `@PreAuthorize/@Secured/@RolesAllowed` extraction + security-config
  cross-ref, instead of the always-empty free-text field.

Each V item = a registry objective + detail keys + an M3 fixture; prioritize by
what your real target services actually use.

---

# Recommended sequencing (accuracy-first)

1. **M (measurement foundation)** — `label` loop + `Language` field + 2–3 real
   fixtures + variance + floor-recall. Nothing else is trustworthy without it.
2. **E1 (Spring phantom routes) + E2 (array topics)** — the floor is currently
   emitting confident WRONG facts that poison the LLM and the output; fix before
   anything that builds on the floor. Cheap, high-impact, table-tested.
3. **P1–P3 (enums + boundaries) & C1–C2 (path canonicalization, detail pin)** —
   cheapest, biggest accuracy/variance wins; stop the duplicates that corrupt
   measurement. Verify the gain on M immediately.
4. **V3a (deterministic config precedence) + V3b (YAML decoder) + X6 (drop dead
   infra call)** — kills a non-LLM variance source and a free token sink in one
   pass over the config layer.
5. **C3–C5 (reexamine deletion, resource keying, delete-class) + E3–E4.**
6. **F1 (cache wiring) → P7 (reconcile backstop) → rest of P (P4–P6).**
7. **A (connection repair + truncation honesty).**
8. **F2–F6 (floor expansion + multi-language detectors)** — drives floor-recall
   up = cost down + stability up; flip fixture flags as each lands.
9. **S (sharding cohesion + keyword seeding + tuning)** — validate recall via M.
10. **X (cost levers: caching, prompt reorder, budget).**
11. **D (sampling controls), measured last.**
12. **V (coverage), demand-driven.**

# Verification (every commit)

- `go build ./... && go test ./...` green; `go test ./internal/eval/...`
  (cheap-mode F1 gate) green as fixtures grow.
- After accuracy changes: `eval --mode score-run` + `--mode variance`
  (core/union ratio) + `--mode floor-recall`, compared to the prior baseline in
  `testdata/eval/history/`.
- End-to-end on `routing-service` (live OpenCode), diff
  `~/.diffmind/runs/<new>` vs `20260608T230315Z`: dangling-exposure count ≪ 18;
  `db_operation.operation ∈ {read,write}` with `raw_operation` preserved;
  outbound_http `service` populated; queue_publish/cli_command counts stable
  across two runs; token cost per the manifest down after X.

# Production-readiness verdict (re-scoped to "functioning, accurate")

Not yet — but the path is concrete and measurable. The order that matters:
**measurement first** (M), then the cheap high-impact accuracy fixes (P+C), then
the structural accuracy gains (A+F+S), then cost (X). "~100%" is not literally
achievable on novel custom code — the honest, *measurable* target is: the
deterministic floor recovers the mechanical majority (tracked by floor-recall),
the LLM covers the custom tail, run-to-run output is stable (tracked by
core/union ratio), and per-language F1 is gated in CI. When those three numbers
are green on real-repo fixtures, it's a trustworthy functioning system.

# Findings ledger (trackable)

Every verified finding from the audits, one row each. **Impact:** A=accuracy,
V=variance/stability, C=cost. **Status:** open (verified, unfixed) /
refuted (investigated, not a bug) / done. Update Status as work lands.

| ID | Finding | File:line | Sev | Impact | Status |
|----|---------|-----------|-----|--------|--------|
| E1 | Spring fabricates phantom routes from produces/consumes/headers/params | `spring.go:166,240` | CRIT | A,V | open |
| E2 | Kafka/Rabbit array-topic name mangled, siblings dropped | `spring.go:86,101,225` | HIGH | A | open |
| E3 | Finder-method op misclassification (all/scan/page → read) | `connections.go:1268` | MED | A | open |
| E4 | `@Scheduled(cron=,zone=)` whole arg stored as schedule | `spring.go:57` | LOW | A | open |
| E5 | `extractFirstStringArg` returns raw arg when unquoted | `spring.go` | LOW | A | open |
| C1 | HTTP path params not canonicalized (`{id}`/`:id`/`<int:id>`) | `deterministic_discovery.go:590`, `eval/identity.go:90` | HIGH | A,V | open |
| C2 | Detail can re-identify entity via Name/details (inv #4) | `detail.go:615,635`, `convert.go:73` | HIGH | A | open |
| C3 | Reexamination deletes true positives on one LLM "no" | `reexamine.go:509,583` | MED | A | open |
| C4 | Schema-qualified resource false-split (`public.orders`) | `reconcile.go:303` | LOW | A | open |
| C5 | `delete` folded into `write` (decide: keep delete class) | `reconcile.go:333`, `connections.go:784,1243` | MED | A | open |
| P1 | `details{}` free-text; no enums → 13 spellings of operation | `schemas.go:16`, `classify.go:59`, `registry.go:253,260` | HIGH | A,V | open |
| P2 | queue_publish key contract mismatch (queue vs destination) | `registry.go:473`, `reexamine.go:73,334` | MED | A | open |
| P3 | No objective-boundary disambiguation (route/webhook, http/aws, db/cache redis, queue/stream) | `registry.go:65,255,367`, `discovery.go:120` | HIGH | A | open |
| P3b | Wrong alias `sqs_consumer→stream_consume` | `classify.go:24` | MED | A | open |
| P4 | Irrelevant AST hints + zero-candidate objectives sharded | `prompts.go:96`, `sharding.go` | MED | A,C | open |
| P5 | Framework bullets not scoped to known frameworks | `prompts.go:279`, `registry.go:233` | LOW | C | open |
| P6 | No empty-return/anti-truncation guidance on high-volume objectives | `registry.go`, `prompts.go` | MED | A,V | open |
| P7 | Emitted fields not canonicalized (op, service=null, platform=database) | `reconcile.go`, `classify.go` | MED | A | open |
| A1 | 40% of exposures get zero connections (DI/async/cron) | `connections.go:298` | HIGH | A | open |
| A2 | Silent connection-walk truncation (no flag) | `scip/walker.go:190-206` | MED | A | open |
| F1 | cache_operation binding detected but not wired | `deterministic_discovery.go:233,252` | MED | A,C | open |
| F2–F6 | No deterministic floor for command_exec/queue_publish/gRPC; JVM-only DB + routes (Go stdlib, Django urls.py, Flask, JAX-RS) | `connections.go:737`, `web.go:128` | HIGH | A,V,C | open |
| S1 | Sharding packs files alphabetically, not by cohesion | `sharding.go:83,103,134` | MED | A,C | open |
| S2 | Detector-less objectives never shard (no candidates) | `sharding.go:90`, `grounding.go:72` | MED | A,V | open |
| S3 | rpc_endpoint fanned into 11 shards (2.7× calls) | `sharding.go:33` | MED | C | open |
| X1 | No cross-call caching (ReuseOpenCodeSession=false) → cold file re-reads | `config.go:110`, `pipeline.go:1349` | HIGH | C | open |
| X2 | Prompts volatile-first → ~250-token cacheable prefix | `prompts.go:327` | MED | C | open |
| X3 | `MaxCatalogItems` dead config; no global token/time budget | `config.go:24`, `pipeline.go:180`, `persist.go:549` | MED | C | open |
| X4 | No per-stage model tiering (one ModelID) | `config.go:8` | LOW | C | open |
| X6 | Infrastructure-stage LLM call output never consumed | `ast_stage.go:154`, `pipeline.go:509` | MED | C | open |
| V3a | Nondeterministic cross-file config resolution (map order) | `config_resolve.go:90` | HIGH | V,A | open |
| V3b | YAML list-of-mappings mis-keyed/collapsed | `parser.go:994,1022` | HIGH | A | open |
| V3c | YAML scalar lists silently dropped | `parser.go:994` | MED | A | open |
| V3d | Test-resource config files pollute the index | `index.go:51,55` | MED | A | open |
| V3e | Mis-indented YAML re-parented silently | `parser.go:1008` | LOW | A | open |
| V1/V2/V4 | Unmodeled types: GraphQL/WS/SSE/serverless triggers; storage/secrets/flags; auth/PII | `registry.go:36-420` | HIGH | A | open |
| M* | Only one toy fixture; no real-repo/LLM/variance/floor-recall measurement | `internal/eval/`, `testdata/eval/` | HIGH | A,V,C | open |
| D | No sampling controls (temperature/seed) sent | `client.go:404,491` | MED | V | open |
| — | Connection orphan-drop after dedup | — | — | refuted (dedup precedes connections) |
| — | HTTP base-path concatenation | `spring.go:176` | — | refuted (correct) |
| — | uncountable/status/data resource normalization | `reconcile.go:288` | — | refuted (sound) |
| — | Polyglot language scoping / alias handling / langdetect | `prompts.go:224,250` | — | refuted (correct) |

Process note (tracked, not a finding): `internal/ast/framework/` has **zero test
files** — exactly where E1–E4 live. Every E-fix lands table tests there.

# Deferred (out of scope now, don't lose)

Security (symlink escape, webfetch egress, prompt-injection fencing, disable
mutating tools); reliability hardening (checkpoint version fingerprint so `retry`
across upgrades doesn't blend logic; file-size/OOM guard + parse `recover()`;
refuse unsafe session-reuse+workers combo — partly addressed by X1's per-worker
design); cross-service architecture graph; commit-to-commit diff mode.
