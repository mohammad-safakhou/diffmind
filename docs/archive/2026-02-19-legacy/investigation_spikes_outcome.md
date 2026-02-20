# Investigation Spikes Outcome (Enterprise Accuracy + UX)

## Scope
This report closes the three investigation spikes from `/Users/developer/repos/mine/diffmind/docs/enterprise_accuracy_ux_plan.md`:
1. Dynamic endpoint/client construction patterns.
2. Profile-specific config merge behavior.
3. Dense graph layout strategy for 1k+ nodes.

## Spike 1: Dynamic Endpoint/Client Construction
## Findings
1. Java outbound HTTP extraction is over-matching and under-modeling.
Reason:
- `detectors_semantic_java.go` uses broad regex for any `.put(...)` / `.delete(...)`, not HTTP-client typed calls.
Evidence:
- False HTTP targets: `publisherId`, `name`, `market`, `page`, `size`, `value`, `label`, `fields`, `where`.
- Files include non-HTTP code:
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/java/com/example/cataloguemanagement/service/PublisherService.java:91`
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/java/com/example/cataloguemanagement/service/LdapUserService.java:45`
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/java/com/example/cataloguemanagement/dto/enricher/AccountDtoEnricher.java:52`

2. Feign clients are present but not extracted as outbound dependency edges.
Evidence:
- 8 Feign clients in code:
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/java/com/example/cataloguemanagement/feign/client/PublisherApiClient.java:13`
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/java/com/example/cataloguemanagement/feign/client/OrderInfoApiClient.java:8`
  - etc.
- ExternalCall library distribution in analyzer output contains only `resttemplate-semantic`, not Feign.

3. Queue extraction in Java is too generic and produces false positives.
Reason:
- `.send(...)` pattern currently classifies calls as Kafka/Rabbit without receiver-type checks.
Evidence:
- `CampaignSseEmitters.send(...)` incorrectly contributes queue events:
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/java/com/example/cataloguemanagement/sync/CampaignSseEmitters.java:48`

4. Queue targets are unresolved (`unknown-*`) even when configured.
Reason:
- SQS extraction expects local literal `queueUrl("...")`, but service uses `@Value` injected variables.
Evidence:
- Output contains `sqs:unknown-queue`, `kafka:unknown-topic`, `rabbitmq:unknown`.
- Actual configured values exist in:
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/resources/application-local.yml:18`
  - `/Users/developer/repos/mine/diffmind/checkout-service/src/main/resources/application-prod.yml:4`

5. Test files contaminate production architecture output.
Evidence:
- ExternalCall facts split exactly 45 from `src/main` and 45 from `src/test`.
- This materially affects dependency graph truth.

## What Must Be Done
1. Implement Java typed call resolution (AST + symbol/type checks), not regex-only call-name matching.
2. Add dedicated Feign extractor:
- parse `@FeignClient(url=...)`,
- parse method mappings (`@GetMapping`, etc),
- emit normalized ExternalCall with `target_service`, `base_url_ref`, `path`, `method`.
3. Add queue extractor receiver/type guards:
- Kafka only for `KafkaTemplate` send,
- Rabbit only for `RabbitTemplate` convertAndSend/send,
- SQS only for `SqsClient` sendMessage/receiveMessage.
4. Add property-link resolver:
- resolve `@Value("${...}")` keys to config values per profile,
- backfill queue/API call targets.
5. Add extraction scope policy:
- default enterprise mode excludes `src/test/**` from architecture graph,
- optional include-test mode for QA analysis.

## Validation To Close Spike 1
1. On `checkout-service`, no ExternalCall target equals `value|label|fields|where|publisherId|page|size`.
2. Feign clients produce outbound dependency edges with resolved base URL key + path.
3. Queue edges resolve to concrete SQS/SNS/topic names (no `unknown-*` for configured channels).
4. Main graph and include-test graph are both available and clearly separated.

## Spike 2: Profile-Specific Config Merge Behavior
## Findings
1. `application-local.yml`, `application-stage.yml`, `application-prod.yml` are not parsed as config manifests.
Reason:
- `isConfigManifestFile` matches `application.` prefix but not `application-`.
Evidence:
- ConfigKey counts from analyzer output:
  - `application.yml`: 136
  - `application-local.yml`: 0
  - `application-stage.yml`: 0
  - `application-prod.yml`: 0

2. Environment inference is heuristic and incorrect for real Spring profile semantics.
Reason:
- `inferEnvironmentScope` is path substring-based.
- No explicit handling for `local`.
- `preproduction` can be misclassified by `contains("prod")`.

3. No deterministic Spring merge/resolution pipeline.
Missing:
- base + profile overlay logic,
- property placeholder/default resolution,
- provenance of resolved values per profile.

4. Config->architecture linkage is incomplete.
Consequence:
- service URLs and queue endpoints from profile files are not reliably attached to ExternalCall edges.

## What Must Be Done
1. Add Spring config loader module:
- parse `application.yml`, `application-<profile>.yml`, `application.properties`,
- merge by Spring precedence rules per target profile (`local`, `stage`, `prod`).
2. Add placeholder resolver:
- resolve `${ENV:default}` style values with environment context,
- mark unresolved placeholders explicitly.
3. Replace heuristic `inferEnvironmentScope` for Spring paths with explicit profile metadata.
4. Emit typed config facts:
- `config_key`, `config_value_ref`, `profile`, `origin_file`, `resolved_value_hash` (and value under policy).
5. Link code property references to resolved config entries.

## Validation To Close Spike 2
1. Resolved config report exists per profile (`local|stage|prod`) with provenance.
2. SQS/SNS/service URL keys in code resolve to concrete profile values.
3. Queue/API dependency edges differ correctly by profile where config differs.

## Spike 3: Dense Graph Layout Strategy (1k+ Nodes)
## Findings
1. Signal-to-noise ratio is currently too low in default view.
Evidence from latest graph:
- Nodes: 713, edges: 1272.
- Architecture-flow edges only 89 (~7% of edges).
- Dominant edges:
  - `config_scoped_to_environment`: 386
  - `service_uses_config`: 386

2. Even topology mode still carries heavy context clutter.
Evidence:
- After topology filter: 595 nodes, 520 edges.
- Still dominated by config/environment/context edges.

3. Current single-scene rendering lacks progressive disclosure.
Consequence:
- high edge overlap,
- hard path-tracing,
- low information density for architecture reasoning.

## Decision
Adopt a 3-layer graph product model:
1. `Architecture` layer (default):
- exposures + logic flow + dependencies only.
2. `Context` layer (optional):
- config/environment/ownership metadata.
3. `Assurance` layer (optional):
- conflicts, verification decisions, risks.

## What Must Be Done
1. Add semantic view presets to API and UI:
- `architecture`, `context`, `assurance`, `full`.
2. Add hierarchical grouping + expand-on-demand:
- section -> class -> entity.
3. Introduce stable layered layout engine for architecture view (ELK layered recommended).
4. Keep LOD behavior:
- hide edge labels/secondary edges at low zoom,
- render neighborhood on focus,
- keep full details in side panel and on-demand expansion.
5. Add search-first and path-first interaction:
- global search,
- “trace from selected exposure to dependencies”.

## Validation To Close Spike 3
1. On `checkout-service`, default architecture view contains no ownership/config flood by default.
2. Task-based UX checks pass:
- find all schedulers,
- trace one endpoint to dependencies,
- identify all queue consumers/publishers,
- export focused subgraph.
3. Performance checks on dense graph:
- smooth pan/zoom,
- no interaction freeze on initial load.

## Final Outcome
The unknowns are now sufficiently resolved to move from investigation to implementation.

Immediate implementation order:
1. Spike 2 foundation first (config resolver and profile merge), because Spike 1 target resolution depends on it.
2. Spike 1 extractor corrections (typed Java + Feign + queue receiver typing + test-scope policy).
3. Spike 3 UI graph layering and layout changes using new edge taxonomy.
