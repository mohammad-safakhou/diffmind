# Synthetic enterprise showcase

This fixture answers a practical question: what does DiffMind do when a company
has ten teams with fifteen services each?

![A readable team scope inside a generated 150-service workspace](../../docs/assets/readme/enterprise-overview.png)

It creates a public-safe workspace containing:

- 10 teams and 150 service repositories;
- 30 databases, caches and event-stream resources;
- 230 HTTP, RPC, async, database and cache relationships;
- deterministic but generated language, file-count and line-count metadata;
- team-local, cross-team and external-service connections.

The topology and metrics are synthetic. This is intentionally a navigation,
layout and scale fixture—not evidence of detector coverage. Use Demo Shop for a
small end-to-end extraction story and the pinned public benchmarks for framework
coverage measurements.

Create it in a new directory:

```bash
make enterprise-showcase DEST=/tmp/diffmind-enterprise
DIFFMIND_HOME=/tmp/diffmind-enterprise ./bin/diffmind ui --no-spa-rebuild
```

Open `http://127.0.0.1:8090`. A workspace this large opens on one team, showing
`15 of 150 services`, rather than shrinking every label into an unreadable
company-wide hairball. Use the team selector to switch bounded contexts, choose
**Team + connected** for immediate cross-team dependencies, search for any
service, or choose **All teams** for the portfolio map.

The generator uses seed `20260910` by default. Override it to vary metrics while
preserving topology counts:

```bash
GOCACHE="$PWD/.gocache" go run ./scripts/enterprise-showcase \
  --home /tmp/diffmind-enterprise-alt \
  --seed 42
```

The generator refuses a non-empty destination. It never deletes or overwrites an
existing workspace.
