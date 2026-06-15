# Discovery After Detail Removal

## Current ownership

The June 2026 pipeline has no LLM detail stage. The entity lifecycle is:

```text
AST floor + LLM objective discovery
  -> keep-biased reexamination of low-signal candidates
  -> deterministic seed-to-entity conversion
  -> AST backfills and client-instance propagation
  -> connections + constrained repair
  -> reconcile
```

Discovery therefore owns every semantic field needed by identity, graph
construction, artifacts, and evaluation. A later stage will not repair an
ambiguous name, missing resource, weak location, or invented evidence.

## June 15 scenario evidence

Five runs of `routing-service` exercised the main options. The source
snapshot changed slightly across the series (240-242 indexed files), and LLM
sampling is not pinned, so these are diagnostic observations rather than an
accuracy benchmark.

| Run | Scenario | Total tokens | Discovery | Detail | Repair | Exposures | Dependencies | Connections |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `20260615T111138Z` | detail enabled | 2,469,561 | 1,402,680 | 779,048 | 137,706 | 48 | 62 | 119 |
| `20260615T120036Z` | skip detail | 1,628,920 | 1,354,268 | 0 | 187,653 | 49 | 59 | 60 |
| `20260615T122931Z` | skip detail + reask | 1,750,589 | 1,486,835 | 0 | 187,538 | 51 | 67 | 117 |
| `20260615T125656Z` | skip detail + ksample | 1,945,340 | 1,675,704 | 0 | 178,843 | 50 | 78 | 120 |
| `20260615T132657Z` | skip detail + framework scope | 1,695,124 | 1,328,742 | 0 | 236,867 | 49 | 63 | 97 |

Conclusions:

1. Detail itself was 31.5% of the baseline token total. Its removal is a clear
   performance win.
2. Entity and connection counts vary too much to use count preservation as an
   accuracy claim. The deterministic-floored HTTP routes and scheduled jobs were
   stable; LLM-owned objectives were not.
3. `reask` and especially `ksample` can add recall, but union sampling also adds
   noise and substantial discovery cost.
4. Structured-output compliance is not semantic validity. Runs emitted
   `placeholder`, `none`, `__none__`, `dummy`, and `noop` rows whose summaries
   explicitly said no item existed.
5. Shard evidence was too loose. A repository with no RPC endpoints produced 12
   `rpc_endpoint` shards because generic Spring `@Service` classes counted as RPC
   candidates. Similar false fan-out affected webhook, stream, and command
   objectives.
6. Connection repair recovered 13-30 final links per run but consumed
   138k-237k tokens. Its precision and marginal value need labels.

## Priority work

### P0: measure the real target

- Add hand-labeled full-run fixtures for large Java, Go, Python, and TypeScript
  services, including negative objectives.
- Run each configuration K times and report per-objective precision, recall, F1,
  semantic-key Jaccard stability, count variance, tokens, and wall time.
- Track deterministic floor, LLM additions, reexamination decisions, repair
  additions, and final survivors separately.

Without this, prompt A/Bs optimize counts and anecdotes rather than accuracy.

### P0: validate candidates before keep-biased retention

- Reject explicit no-result sentinels at discovery ingress.
- Confirm every location exists, the line is in range, and the evidence snippet
  matches source near that range.
- Treat line 1 directory/package citations and negative prose as weak evidence,
  not corroboration.
- Record rejection reason codes and per-objective sentinel/evidence failure rates.

Keep-biased reexamination is appropriate only after evidence is independently
credible.

### P1: separate hint recall from shard permission

Prompt hints may be loose because irrelevant context costs a few tokens. Shard
permission must be strict because one false candidate can create many expensive
calls.

- Maintain objective-specific shard evidence predicates distinct from prompt
  hint predicates.
- Prefer framework bindings, known imports/call receivers, and narrow
  annotations over generic class-name substrings.
- Emit shard planning metrics: candidate weight, files, directories, and the
  evidence rule that authorized fan-out.
- Add negative fixtures such as Spring services with no RPC, REST controllers
  with no webhooks, and processors with no stream API.

### P1: make objective identity a structured contract

- Require identity-bearing fields per objective in the structured schema:
  method/path, resource/operation kind, queue/topic, service/method, command,
  schedule, and client anchor.
- Keep `details` extensible, but normalize required identity fields immediately.
- Use the same canonical operation/resource/path logic in shard merge,
  verification merge, reconcile, and eval.
- Add cross-objective conflict checks before conversion for route/webhook,
  db/cache, queue/RPC, and queue-consumer/stream-consumer pairs.

### P1: candidate-manifest discovery

The long-term prompt shape should be "classify and complete this bounded
candidate manifest, then search a small explicit dynamic tail", not "find ALL X
in the repository".

- Build per-objective manifests from bindings, symbols, imports, calls, config,
  generated specs, and deployment files.
- Cluster by semantic resource/client rather than directory alone.
- Let shards read shared code globally while reporting only owned candidates.
- Run a final cheap coverage check against the manifest to identify candidates
  neither accepted nor explicitly rejected.

### P1: redesign verification

`reask` does nothing when the first pass returns zero. `ksample` repeats the most
expensive whole-repo prompt and unions noise.

- Verify missed manifest candidates, not the entire repository.
- Separate recall expansion from precision judging.
- Require new findings to pass independent source validation.
- Enable verification only when measured marginal F1 per token is positive for
  that objective/repository shape.

### P2: extend deterministic recall carefully

Highest-value targets:

- External cache operations with proven external backing.
- CLI entrypoints, RPC endpoints, and webhook registrations.
- Non-Feign HTTP clients and more queue/stream libraries.
- Connection clients derived from declarations plus config anchors.
- Remaining ORM/database patterns by language.

Every detector needs positive and negative fixtures; wrong deterministic facts
are worse than missing ones.

### P2: reduce cost and latency

- Add per-run and per-objective token/call budgets with explicit failure or
  degraded-result status.
- Stop additional shards/samples on diminishing returns.
- Use cheaper models for candidate classification and retain the strongest model
  for ambiguous semantic tails.
- Derive repo facts deterministically first and ask the LLM only for unresolved
  stack facts.
- Shortlist dependencies for connection repair by file/module/call-graph
  proximity and split large repair catalogs.
- Benchmark worker counts; 20 concurrent objective workers may increase provider
  contention and tool duplication even when wall time looks better.

## Required telemetry

Every run should persist:

- Effective discovery flags/model/sampling settings.
- Calls, tokens, latency, candidates, accepted items, rejected items, and
  deterministic coverage per objective.
- Shard authorization evidence and yield.
- Sentinel and invalid-evidence rejection counts.
- Reexamination confirmed/doubted/rejected counts by trigger.
- Exposures left unconnected before and after repair.
- Final provenance distribution (`deterministic`, `llm`, `llm_repair`).

These metrics make future accuracy and performance work testable instead of
prompt-driven guesswork.
