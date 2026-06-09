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
- **~~`label promote`~~ — DROPPED (reviewer, valid).** The original idea was to
  auto-accept floor output as ground truth for "high-precision" types. Rejected:
  the floor cannot be its own oracle — and E1/E2 prove it emits false positives
  (phantom routes), so promotion would bake those into "truth." `label init`
  already pre-fills floor items as *candidates*; the human confirms via `diff`.
  Cost-reduction is preserved by init+diff; no auto-truth. Ground truth is
  human-reviewed only.

### M2 — Label format extensions (`internal/eval/label.go`)
Add `Language string` (enables per-language P/R/F1; never enters `identityKey`)
and `Verified string` ("human" | "unconfirmed") for honest provenance. Keep
line-numbers/details OUT (the matcher is intentionally path/line-insensitive).
**Reviewer correction (valid):** a `Language` tag on *labels* alone does not
enable per-language scoring — **extracted entities also need language
attribution** (so extraction output can be bucketed by language, not just the
labels). Fixture-level `languages:[...]` is sufficient only for *monolingual*
fixtures; polyglot fixtures need per-entity language on both sides.

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

### M5 — Floor-**coverage** metric (renamed; reviewer, valid) (new `internal/eval/floor_coverage.go`)
`--mode floor-coverage`: run `DeterministicFloor` and load the full LLM run for
the same repo; per type compute `|floor_keys ∩ llm_keys| / |llm_keys|`. **This is
NOT recall** — the LLM run is not ground truth, so this measures floor↔LLM
*overlap*, a cheap operational signal available before labels exist. **True floor
recall requires human labels** (`|floor ∩ labels| / |labels|`) and is reported
separately once M-labels exist. Keep both: coverage now, recall once labeled.

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
`registry.go:253,260` with one canonical mapping line. The schema enum is what
actually *enforces*; the prose alone drifts.
**Reviewer (valid): decide the operation ontology BEFORE adding enums** — see C5
(recommended `operation_kind ∈ {read,write}` + preserved raw CRUD). The run holds
~17 distinct `details.operation` strings (not 13); the exact count is immaterial,
the ontology decision is the blocker.

### P2 — Fix the `queue_publish` key contract
Example/DetailKeys teach `queue` (`registry.go:473`) but the reexamine gate
requires `destination` (`reexamine.go:73`); it only passes via a fragile alias
(`reexamine.go:334`). Pick ONE canonical key (`destination`), make example +
DetailKeys + gate agree, state it explicitly. Audit all of `reexamine.go:62-77`
required keys against the registry examples for the same mismatch.

### P3 — `OBJECTIVE_BOUNDARIES` disambiguation block
Discovery objectives run in parallel, each blind to its siblings → double-claims
and gaps. Add a shared 1-line-per-sibling boundary block to `buildDiscoveryPrompt`:
- **http_route vs webhook:** webhook = provider-callback endpoints. **Reviewer
  correction (valid):** do NOT require signature verification — unsigned external
  callbacks are still webhooks (internal providers, unsigned integrations). Use
  "receives a third-party/provider callback" as the criterion, signature as a
  strong-but-not-required signal.
- **outbound_http vs aws-sdk:** outbound_http EXCLUDES AWS SDK calls only to
  transports that HAVE a home objective — SQS/SNS→queue, DynamoDB→db,
  Kinesis→stream (the run already needed `dropTransportDuplicates`,
  `reconcile.go:349`). **Reviewer correction (valid): do NOT exclude S3** (or
  other object storage) until a V2 object-storage objective exists — excluding it
  now drops it entirely (a coverage hole), since nothing else catches it.
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

### P6 — Anti-truncation (CORRECTED by reviewer)
**The empty-return half was wrong:** every discovery prompt already includes
`If nothing matches, return {"items": []}` via the shared HARD RULES
(`prompts.go:356`) — it's not missing. Drop that item. **And the "prioritize ~50
items" idea is the wrong fix** (it knowingly sacrifices recall, which violates
accuracy-first). Correct approach: on a large objective, **shard or retry** to
cover everything; never silently cut JSON; log/flag when output looks truncated.

### P7 — Reconcile backstop for canonicalization (defense in depth)
Even with P1 enums, keep a deterministic backstop in `reconcile`: **add canonical
fields alongside the raw ones, never rewrite evidence** (reviewer, valid) —
canonicalize `operation`→`operation_kind` while preserving the raw/CRUD value;
resolve name/operation contradictions; infer specific `platform` from
driver/datasource hints instead of generic `"database"`. Eval-safe (idempotent
with `SemanticKeyLoose`); add a consistency test.
**CORRECTION:** the "derive outbound_http.service — all 10 were null" justification
is STALE/WRONG. All 10 outbound_http already have a resolved target in
`details.target_service` + `instance`; only the unused top-level `service` field
is mis-populated (it holds the repo path). Drop the service-derivation item;
optionally fix the top-level field, low value.

---

# Workstream C — Correctness bugs that corrupt accuracy/measurement

Fix these early — they create duplicates and data loss that would poison the
eval signal.

- **C1 — HTTP path params never canonicalized (duplicates + eval FP/FN).** No
  code maps `{id}`↔`:id`↔`<int:id>`↔`*`; identity uses verbatim path
  (`deterministic_discovery.go:590`, `eval/identity.go:90`). Add a
  `canonicalizeRoutePath` and apply it in **all three identity sites — discovery
  merge, reconcile, and eval** (reviewer, valid) so the same canonical form is
  used everywhere. *(Base-path concat is already correct — `joinPath`,
  `spring.go:176`.)*
- **C2 — Detail re-identification: PARTIALLY STALE (reviewer, verified).** Names
  are already pinned: `pipeline.go:727` preserves the discovered type/name on the
  seed-fallback path. The **remaining** risk is narrower — identity-bearing
  *details* (path/operation/table) can still be overwritten by `mergeEnrichment`
  (`detail.go:635`), which changes the dedup/eval key even when the name is
  stable. Fix scope: pin identity-bearing details (not the name) to the seed;
  regression-test that the dedup key is invariant across detail.
- **C3 — Reexamination deletes true positives (data loss).** One LLM "no" drops
  a real low-confidence item (`reexamine.go:509,583`). Require corroboration
  before deletion, or downgrade-don't-drop; never let one conservative "no" erase
  discovered architecture. (Note: in the audited run reexamination made 0 calls —
  `deriveDetailsFromName` cleared all suspects — so this is latent, fires on
  noisier repos.)
- **C4 — Schema-qualified resource false-split (duplicate).** `public.orders` vs
  deterministic `orders` → different keys (`reconcile.go:303`). Strip schema
  qualifier + unify snake/camel before keying.
- **C5 — `delete` folded into `write` → explicit product decision (reviewer).**
  `normalizeDBOp` (`reconcile.go:333`), `inferDBOperationKind*`
  (`connections.go:784,1243`) collapse delete+insert+update → write.
  **Recommended resolution (reviewer + agreed):** canonical `operation_kind ∈
  {read,write}` for dedup/identity stability, PLUS a preserved finer
  CRUD/`raw_operation` field for fidelity. This is the ontology that P1's enum
  must encode — **decide it before P1** (see Open debate below).

---

# Workstream E — Deterministic extraction correctness (fix the floor before growing it)

A wrong deterministic fact is merged into output AND injected into the LLM as
"KNOWN_CONFIRMED_ITEMS — do not rediscover," so a precision bug here is the worst
kind: it corrupts output and misleads the LLM. These are verified (probe-tested),
high-frequency, and directly violate invariant #6. **Fix before expanding the
floor (F).** Test home: framework tests **do** exist at
`internal/ast/framework_hardening_test.go` (reviewer correction — my agent looked
in `internal/ast/framework/` and missed the parent package); the E1/E2 cases are
just missing. Add them there.

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

- **F1 — cache_operation: REFUTED as "near-free wiring" (reviewer, VERIFIED).**
  Spring emits **no** cache bindings today — there is no `@Cacheable/@CachePut/
  @CacheEvict` detection anywhere in `internal/ast/framework/` (verified by grep).
  So this is **new detector work** (add the annotation detector in `spring.go`
  AND wire the binding kind into `objectiveForBinding`/`supportedDeterministicObjectives`,
  `deterministic_discovery.go:233,252`), not a one-line wiring. Re-rank it with
  F2–F4 accordingly, not as a quick win.
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

**CORRECTED NUMBERS (reviewer + re-verified across all 8 connection files):**
**13** of 45 exposures got zero connections — **8 http_routes + 5 scheduled_jobs;
all 3 queue_consumers ARE connected.** (My earlier "18, incl. all queue
consumers" was an evidence error from a script that read only 1 of 8 connection
files.) The gap is real but smaller and is about routes/cron, not consumers.
Cause: the AST BFS (`connections.go:298`) can't cross DI/interface, async/event,
or some cron boundaries.

- **A1 — LLM connection repair tail (Stage 4.5)**, additive, after the
  deterministic walk. **MVP: exposures with zero connections** (clearest win, no
  merge/double-count complexity). **Reviewer (valid, future): also repair
  INCOMPLETE connection sets** — deferred because "incomplete" needs a detection
  signal we don't yet have (you can't see what's missing); see Open debate. The
  LLM picks targets from a **closed set** of existing dependency IDs
  (hallucinations rejected, orphan-dropped by `reconcile.FilterConnections`).
  Tagged `llm_repair`, confidence-clamped, checkpointed, fail-soft. New
  `internal/agents/connection_repair.go`; integrate before reconcile
  (`pipeline.go:~800`). *(Safe to measure once C1/C2 fix identity.)*
  **Reviewer (valid): validate repair output by its EVIDENCE** (cited file:line
  exists and is plausible), **not by re-confirming via the AST path that already
  failed** — requiring the AST walk the repair exists to recover would be circular.
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

- **X1 — Cross-call caching (biggest cost win) — but NOT accuracy-neutral
  (reviewer).** Today `ReuseOpenCodeSession=false` (`config.go:110`); the
  4.52M cache_read (manifest, not 6.6M) is within-call only. The same
  build/config/base files are re-read cold across 35 discovery + 10 detail calls
  (input is 67% of cost, mostly repeated file reads). **Reviewer (valid):
  reusing sessions can contaminate one objective with another's context — that's
  an accuracy risk, not just concurrency.** So do NOT reuse sessions for caching;
  prefer **explicit provider prompt-caching** (cache-control breakpoint on the
  stable repo context) or **isolated per-worker contexts**. ⚠️ Open: whether
  OpenCode's API exposes an explicit cache breakpoint is unverified (see Open
  debate); the no-API-dependency lever is X2 (prefix reordering → provider auto
  prefix-cache).
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
- **X6 — Infrastructure stage: NOT fully dead (reviewer correction).**
  `runInfrastructureStage` (`ast_stage.go:154`) persists `state/infrastructure.json`,
  which the **UI reads** — so it's not dead, just unused by *core extraction*
  (`pipeline.go:509` `_ = infra`). **Fix:** make it optional (skip when the UI
  isn't needed / behind a flag) OR wire it into the detail stage for endpoint
  resolution (the original intent). Don't blindly delete.

---

# Workstream D — Determinism / sampling controls (measured, not assumed)

The client sends no sampling controls today (`client.go:404,491`). **Reviewer
(needs confirmation): adding `temperature`/`top_p`/`seed` directly to the message
body is NOT supported by OpenCode's documented API — temperature/top-p are set
via OpenCode *agent configuration*, and `seed` is provider-specific.** This is
the one claim I could not verify from this repo (it's about the external server's
API) — confirm against the OpenCode version in use before redesigning D around
agent-config. Either way, sequence LAST and judge purely by the M4 variance
harness, so it measures residual variance after F/P/S rather than masking whether
those were the real stabilizers.

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

(Reconciled with reviewer's recommended order.)

1. **E1 + E2 (Spring phantom routes, array topics)** with focused regression
   tests — the floor emits confident WRONG facts today; fix first so the first
   labels and floor-coverage numbers aren't capturing phantoms.
2. **Minimal trustworthy M** — `label init/diff` (**human-reviewed, no
   floor-promote**), variance harness, label-based floor *recall* (+ cheap
   floor↔LLM coverage). Per-entity language attribution (M2). Nothing downstream
   is trustworthy without it.
3. **Operation taxonomy decision (C5)** → THEN **P1/P2/P3 (enums + boundaries)
   and C1/C2** (shared route canonicalization; pin identity-details). Verify the
   gain on M.
4. **V3 config correctness** (V3a precedence + V3b/d YAML decoder + test-path
   filter) — kills a non-LLM variance source.
5. **C3 (corroborate-before-reject), C4 (schema-identity — see Open debate), E3–E4.**
6. **A1/A2 (connection repair, evidence-validated + truncation honesty).**
7. **Floor expansion + measured sharding** — F1 (cache detector, NOT wiring) +
   F2–F6 + S1–S3 (sharding, hub-safe clustering, validated via M).
8. **Cost controls (X, incl. X6 make-optional) → sampling experiments (D, after
   API capability check) → coverage expansion (V).**

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

# Open debates (need alignment with reviewer — NOT settled)

These are the points where I did **not** simply accept the review — each has a
real counter-argument or an unresolved tension worth discussing before we build.

1. **C4 — schema-qualified table identity is a genuine two-sided tension, not a
   one-way fix.** The reviewer is right that blindly *stripping* schemas would
   wrongly MERGE `public.orders` and `audit.orders`. But my original C4 was about
   the opposite failure that's also real in the run: the LLM emits `public.orders`
   while the deterministic deriver emits `orders`, so the *same* table FALSE-SPLITS
   into two db_operations. You cannot fix both by "always strip" or "always keep."
   **Proposed resolution to debate:** treat the schema as part of identity, but
   make a schema-less name a *wildcard* that matches a single schema-qualified
   counterpart (merge `orders`↔`public.orders` only when `public` is the lone
   schema seen; keep `public.orders`≠`audit.orders`). Needs sign-off.
2. **A1 scope — zero-connection MVP vs incomplete-connection repair.** Agreed
   incomplete-connection repair is the eventual goal, but it needs a *signal that
   a connection set is incomplete*, which we don't have (you can't validate
   against connections you don't know are missing). Propose: ship zero-connection
   repair first; design an "incompleteness" heuristic (e.g. exposure reaches deps
   the LLM lists in its details but the walk didn't) as a follow-up. Agree on
   MVP-first?
3. **X1 / D — both presuppose OpenCode API capabilities we have NOT verified.**
   "Explicit prompt caching" (X1) and "sampling via agent config" (D) are the
   reviewer's recommended mechanisms, but neither is confirmed against the
   OpenCode version we run. Before committing: confirm (a) does OpenCode expose a
   prompt cache-control breakpoint? (b) does it accept temperature/top-p/seed via
   agent config, and does the provider honor seed? If not, the only verified
   levers are X2 (prompt reordering for provider auto prefix-cache) and accepting
   residual variance. Action: a short OpenCode-API capability spike feeds both.
4. **M1 — agreed (drop promote), one note:** the labeling-cost goal is fully met
   by `init`+`diff`; dropping `promote` costs us nothing, so this is not really a
   tradeoff. Flagging only so the reviewer knows we didn't lose the cost win.

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
| C2 | Detail re-identification — names already pinned (`pipeline.go:727`); only identity-details overwrite remains | `detail.go:635` | MED | A | partially stale |
| C3 | Reexamination deletes true positives on one LLM "no" | `reexamine.go:509,583` | MED | A | open |
| C4 | Schema-qualified resource false-split (`public.orders`) | `reconcile.go:303` | LOW | A | open |
| C5 | `delete` folded into `write` (decide: keep delete class) | `reconcile.go:333`, `connections.go:784,1243` | MED | A | open |
| P1 | `details{}` free-text; no enums → 13 spellings of operation | `schemas.go:16`, `classify.go:59`, `registry.go:253,260` | HIGH | A,V | open |
| P2 | queue_publish key contract mismatch (queue vs destination) | `registry.go:473`, `reexamine.go:73,334` | MED | A | open |
| P3 | No objective-boundary disambiguation (route/webhook, http/aws, db/cache redis, queue/stream) | `registry.go:65,255,367`, `discovery.go:120` | HIGH | A | open |
| P3b | Wrong alias `sqs_consumer→stream_consume` | `classify.go:24` | MED | A | open |
| P4 | Irrelevant AST hints + zero-candidate objectives sharded | `prompts.go:96`, `sharding.go` | MED | A,C | open |
| P5 | Framework bullets not scoped to known frameworks | `prompts.go:279`, `registry.go:233` | LOW | C | open |
| P6 | Empty-return ALREADY present (`prompts.go:356`); real fix = shard/retry, never truncate | `prompts.go:356` | MED | A,V | corrected |
| P7 | Canonicalize op (add fields, keep raw); platform=database. outbound_http service already resolved — that part STALE | `reconcile.go`, `classify.go` | MED | A | partial |
| A1 | 13 (not 18) exposures unconnected: 8 routes + 5 cron; queue consumers ALL connected | `connections.go:298` | MED-HIGH | A | open (numbers corrected) |
| A2 | Silent connection-walk truncation (no flag) | `scip/walker.go:190-206` | MED | A | open |
| F1 | cache_operation: Spring emits NO cache bindings — needs a DETECTOR, not wiring | `spring.go`, `deterministic_discovery.go:233,252` | MED | A,C | reworked (was wrong) |
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

Process note (corrected): framework tests exist at
`internal/ast/framework_hardening_test.go` (parent package); the E1–E4 cases are
just missing. Add them there.

**Run-fact corrections (reviewer, verified) — applies to all run-derived claims:**
- Cache-read = **4,516,224** (manifest stage sum), not 6.6M.
- **13** exposures unconnected (8 routes + 5 cron); all queue consumers connected.
- All 10 outbound_http have a resolved `target_service`/`instance`.
- Manifest = 47 token-counted calls; events show 48 call/session cycles (one
  fallback lacked token counters).
- Variance claims (cli_command 7→0, command_exec 1→0, outbound_rpc 14→10,
  stream_consume 0→1) confirmed.
- `run_manifest.json` has **no git SHA**, so the run cannot be pinned to an exact
  revision (it finished 01:24 Berlin, before commits at 01:40–16:45). **Add a git
  SHA to the manifest** so future eval/variance runs are reproducible.

# Deferred (out of scope now, don't lose)

Security (symlink escape, webfetch egress, prompt-injection fencing, disable
mutating tools); reliability hardening (checkpoint version fingerprint so `retry`
across upgrades doesn't blend logic; file-size/OOM guard + parse `recover()`;
refuse unsafe session-reuse+workers combo — partly addressed by X1's per-worker
design); cross-service architecture graph; commit-to-commit diff mode.
