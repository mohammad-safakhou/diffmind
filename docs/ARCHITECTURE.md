# Architecture

DiffMind has three internal layers behind one command and one persistent home.

1. The extractor reads one source repository and emits a deterministic
   `diffmind.service.v1` document with observations and evidence.
2. Deterministic knowledge packs translate organization-specific file
   conventions into service identities and resolution aliases.
3. The workspace owns projects and repositories, schedules extraction runs,
   and resolves service documents into a cross-repository graph.
4. The web UI exposes project setup, run progress, graph exploration, and
   impact analysis through the workspace API.
5. A shared read-only query layer exposes persisted graphs through `/api/v1`,
   stdio MCP, and streamable HTTP MCP at `/mcp`, so browsers, integrations, and
   coding agents use the same graph semantics.
6. The company server periodically syncs registered Git repositories, runs the
   extractor concurrently, and publishes a new graph. Overlapping fleet
   refreshes are rejected so one deployment cannot race itself.

The protocol package is the boundary between extraction and graph assembly.
It contains no I/O policy beyond document encoding, schema generation, and
validation.

## Local data

```text
~/.diffmind/
├── config.json
├── diffmind-packs.lock
├── packs/
├── runs/
└── projects/
    └── <project-id>/
        ├── project.json
        ├── repos/
        ├── packs/
        └── runs/
```

Set `DIFFMIND_HOME` to relocate the entire tree. Repository analysis is
deterministic by default; generated facts should always retain concrete source
evidence.

## Deployment boundaries

The local default binds to loopback without authentication. Binding to a
non-loopback address requires `DIFFMIND_AUTH_TOKEN` or
`DIFFMIND_TRUSTED_PROXY_SECRET`; bypassing that guard needs the explicit
`--allow-unauthenticated` flag. The same middleware protects the SPA, mutation
APIs, query API, event streams, and remote MCP transport, while `/healthz` is
intentionally public. Trusted-proxy identities carry viewer, editor, or admin
roles, and non-read requests are persisted to the HTTP audit log.

The application container runs without root privileges or Linux capabilities.
Its only durable writable path is `/data`, which is the container's
`DIFFMIND_HOME`. TLS and OIDC login belong at the reverse-proxy or ingress
boundary; DiffMind validates the proxy secret, enforces roles, and records the
resulting stable user identity.

## Knowledge precedence

Identity is assembled deterministically in this order:

1. A repository-owned `.diffmind/service.yaml` override.
2. Matching project and globally installed packs, ordered by explicit priority.
3. The repository name as a safe fallback.

Equal-priority packs that derive different service names fail the run instead
of silently choosing one. Each graph run records
`knowledge_pack_set_digest`, a digest of the exact pack content used.
