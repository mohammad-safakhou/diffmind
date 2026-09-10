# DiffMind Demo Shop

This synthetic e-commerce system is DiffMind's canonical public demonstration.
Every name, hostname, source file and expected relationship is owned by this
repository and safe to use in screenshots, recordings, tests and documentation.

The generator creates six independent Git repositories:

```text
storefront -> gateway -> catalog -> [catalog-cache]
                     \-> checkout -> payment -> [payments database]
                                  \-> [orders.created] -> notification
```

The applications are deliberately small analysis fixtures. Their source uses
realistic Go, Python/Flask, Java/Spring, OpenFeign and Kafka conventions, but the
demo does not need language package installation or running infrastructure.

## Prepare the repositories

From the DiffMind checkout:

```bash
make build
sh scripts/prepare-showcase.sh /tmp/diffmind-demo-shop
```

The command refuses to overwrite an existing destination. It creates
`repositories/`, an isolated `workspace/`, and commits each service on `main`
with synthetic author details.

Analyze the repositories individually:

```bash
for repo in /tmp/diffmind-demo-shop/repositories/*; do
  service=${repo##*/}
  ./bin/diffmind run --repo "$repo" \
    --out "/tmp/diffmind-demo-shop/analysis/$service"
done
```

For the combined graph, start DiffMind with the generated workspace and import
the `repositories` directory through **Import & build**:

```bash
DIFFMIND_HOME=/tmp/diffmind-demo-shop/workspace \
  ./bin/diffmind ui --no-spa-rebuild
```

The expected graph is recorded in [expected.json](expected.json).

## Contract-change story

The public demo task is:

> Add `deliveryWindow` and `market` to the checkout request and rename
> `postalCode` to `deliveryPostalCode`. Identify affected callers and classify
> compatibility before editing them.

After building the baseline graph, apply and commit the prepared change:

```bash
sh scripts/apply-demo-shop-change.sh /tmp/diffmind-demo-shop
```

Update the graph and compare the two runs. DiffMind should report the rename as
a removal plus an addition, the required `market` field as potentially breaking,
and optional `deliveryWindow` as compatible. Application tests—not the graph—are
the final authority on runtime behavior.

## Public-media rules

- Record only this generated workspace, never a private workspace.
- Keep the synthetic `*.example.test` hostname visible so viewers can recognize
  that it is not a production system.
- Display exact source evidence and uncertainty; do not imply complete runtime
  coverage.
- Regenerate all screenshots and videos when the expected graph changes.
