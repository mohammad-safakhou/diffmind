# Public demo and media

This is the canonical public story for DiffMind. It uses only synthetic Demo
Shop repositories and pinned Apache-2.0 upstream projects. No company source,
service name, hostname, credential or workspace is needed.

![DiffMind public demo overview](assets/readme/diffmind-demo.gif)

## The story in one minute

Demo Shop is six small Git repositories written in Go, Python and Java:

```text
storefront -> gateway -> catalog -> [catalog-cache]
                     \-> checkout -> payment -> [payment-db]
                                  \-> [orders.created] -> notification
```

DiffMind analyzes each repository, resolves cross-service identities and builds
one graph containing:

| Result | Verified value |
| --- | ---: |
| Services | 6 |
| Graph edges | 9 |
| Direct service-to-service edges | 4 |
| Async chains | 1 |
| Shared infrastructure resources | 3 |
| Stale or dirty repositories | 0 |

The graph includes four kinds of evidence-backed relationships: HTTP calls,
Kafka publication/consumption, Redis operations and a database write. A
deliberate external call to `status.example.test` shows how an external service
appears without pretending it is owned by the workspace.

![The complete Demo Shop architecture graph](assets/readme/demo-shop-graph.jpg)

## Reproduce it from a clean checkout

Build DiffMind and generate the six independent repositories:

```bash
make build
sh scripts/prepare-showcase.sh /tmp/diffmind-demo-shop
```

Run the automated public fixture check:

```bash
make test-showcase
```

Start a workspace that contains no private data:

```bash
DIFFMIND_HOME=/tmp/diffmind-demo-shop/workspace \
  ./bin/diffmind ui --no-spa-rebuild
```

Open `http://127.0.0.1:8090`, create a project and choose **Import & build**.
Import `/tmp/diffmind-demo-shop/repositories`. A successful run should match
[`examples/demo-shop/expected.json`](../examples/demo-shop/expected.json).

## Demonstrate change impact

The prepared task evolves `POST /v1/checkout`:

> Add optional `deliveryWindow` and required `market`, then rename required
> `postalCode` to `deliveryPostalCode`.

Apply the scenario after preserving the baseline graph:

```bash
sh scripts/apply-demo-shop-change.sh /tmp/diffmind-demo-shop
```

Choose **Update graph**, then compare the saved runs. The request-contract query
produces four useful decisions:

| Field change | DiffMind classification | Reason |
| --- | --- | --- |
| Add required `deliveryPostalCode` | Potentially breaking | New required input; one half of the rename |
| Add optional `deliveryWindow` | Compatible | Existing callers can omit it |
| Add required `market` | Potentially breaking | Existing callers do not send it |
| Remove `postalCode` | Potentially breaking | Existing callers may still send the old key |

The generic graph comparison deliberately reports changed saved facts rather
than claiming causality. Runtime tests and an API migration policy remain the
final authority.

![Saved graph snapshots compared in the dashboard](assets/readme/graph-comparison.jpg)

## Operate it over time

Runs are durable. The Operations screen records ingestion attempts, refresh
jobs, retries and resource limits, while repository and graph views report
freshness. This matters for agent use: a relationship from an old or dirty
analysis should not be treated as current truth.

![Durable operation and ingestion history](assets/readme/operations-history.jpg)

## Give the graph to an agent

After following [`AGENT_SETUP.md`](../AGENT_SETUP.md), a coding agent can use the
same workspace to:

1. list services and inspect an endpoint or dependency;
2. trace callers before editing a contract;
3. retrieve source locations and confidence instead of inventing an edge;
4. compare request fields across saved runs;
5. report stale, dirty, unresolved or unsupported areas explicitly;
6. propose a tested knowledge-pack or detector contribution for a missing
   company convention.

A useful prompt for Demo Shop is:

> Use DiffMind to inspect `POST /v1/checkout`. Show its inbound caller,
> downstream payment call and published event with source evidence. Compare the
> two saved runs, classify the request-field changes, and state any uncertainty.

## Why there are three public examples

| Example | Purpose | Expected result |
| --- | --- | --- |
| Demo Shop | Stable product walkthrough and media source | Complete known topology and contract story |
| OpenTelemetry Demo | Broad polyglot compatibility benchmark | Finds detector gaps across many languages and frameworks |
| Google Online Boutique | Familiar gRPC-focused benchmark | Exposes protobuf, gRPC and configuration-resolution gaps |

The real-world projects are intentionally not used as polished proof that every
framework is supported. At the pinned revisions, all 44 `src/*` scans and both
whole-monorepo scans completed, but many relationships were not resolved. See
[the benchmark record](../examples/public-benchmarks/README.md) for exact
revisions, counts and the prioritized contribution areas.

## Media provenance and regeneration

The checked-in captures were produced on 10 September 2026 from the generated
`DiffMind Demo Shop` workspace at 1920×1080. They contain synthetic names only.
Frames that exposed an absolute user path were rejected rather than redacted.

The animation is derived from these four captures:

- `project-list.jpg` — local project selection;
- `demo-shop-graph.jpg` — complete six-service graph;
- `graph-comparison.jpg` — saved graph comparison;
- `operations-history.jpg` — durable ingestion history.

After replacing the sanitized JPEG captures, rebuild the GIF with:

```bash
make demo-media
```

Always regenerate from a fresh public-only workspace. Review every frame at full
resolution for usernames, absolute paths, private hostnames and credentials
before committing it.
