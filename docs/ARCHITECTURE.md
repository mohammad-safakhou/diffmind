# Current architecture

Diffmind is a deterministic architecture knowledge platform. The default product
surface is the project workspace, not the legacy single-repository catalog.

```text
Local Git repositories / GitHub organization
                  |
        persisted project ingestion
       sync -> analyze or reuse checkpoint
                  |
      versioned per-repository artifacts
                  |
   identities + knowledge packs + cross-service resolution
                  |
           versioned project graph
             /             \
          web UI       HTTP query API / MCP
```

`internal/extractor` parses source/configuration, runs static detectors and
connection analysis, and emits Protocol documents with source evidence.
`protocol` defines that interchange contract. `internal/workspace` owns project
storage, repository synchronization, packs, graph assembly, queries, and MCP.
`cmd/diffmind` ships both through one executable.

Knowledge packs are declarative data, not arbitrary executable plugins. They
teach service identity, configuration/resource naming, relationship resolution,
and evidence-backed HTTP/RPC/queue declarations through field paths or regexes.
Pack collection and deterministic resolver results feed the graph snapshot read
by both UI and MCP. Framework-specific AST detectors still live in the engine;
packs cannot execute code or introduce new AST analysis algorithms. See the
[tested support matrix](supported-patterns.md).

Local and shared deployments use the same filesystem-backed metadata/artifact
store, with an opt-in indexed SQLite refresh queue. Analysis runs
as cancellable child processes; graph building runs in-process. The shared
profile adds a persisted refresh queue, scheduled/webhook triggers, bounded
project and repository-operation workers, remote MCP, and authentication through
an admin token, scoped project agent token, or trusted identity proxy. It is not a distributed queue or
multi-tenant database service. A data directory must have one server writer.

No OpenCode server or LLM provider is required. Older extractor catalog/design
notes are not the authority for the current platform plan.
See [the roadmap](ROADMAP.md) for completed and remaining work.

## Local data

`DIFFMIND_HOME` (default `~/.diffmind`) contains central `config.json`, the
`diffmind-packs.lock`, installed `packs/`, repository analysis `runs/`, and
`projects/<id>/`. Each project contains `project.json`, latest `ingestion.json`,
`ingestions/<id>/attempt-*.json`, optional `access.json` memberships and
`tokens.json` credential verifiers/history, `limits.json` resource caps, `repos/`, project `packs/`, and versioned graph
`runs/`. Root-level `jobs/` stores JSON refresh jobs and their attempt history;
after explicit offline migration, `queue/queue.sqlite` becomes the authoritative
job store and the original JSON is retained only as historical input. Store
checkpoints use same-directory temporary writes, file sync, and atomic rename.
Offline snapshots preserve these records without rewriting history. Maintenance
leases coordinate the CLI with backup; restore validates in private staging and
never replaces an existing destination. These are not distributed writer locks
or schema migrations. See [recovery](backup-recovery.md).

The SQLite queue uses transactional admission/claims, a per-project active-job
constraint, indexed page queries and attempt aggregates. A private staging
database is verified before atomic no-replace activation. Shared lifecycle code
keeps JSON and SQLite behavior aligned. CLI servers acquire a local singleton
lock before recovery; analyzers/MCP retain shared maintenance access. Queue
transactions do not include external processes or graph files. See
[queue migration and limits](queue-storage.md); distributed workers remain future work.

Project limits bound queued/running refresh admission and simultaneous repository
sync/analyzer work. Queue policy checks share the store's admission lock/transaction.
The server admits repository operations against global and project counters
together, and a change notification wakes waiting work after releases or policy
updates. Reductions drain without rewriting accepted jobs. Policies use atomic
revision-checked files, remain admin-only in either access mode, and are not OS
resource isolation. See [resource limits](operations.md#per-project-resource-limits).

## Deployment boundaries

The local default binds to loopback without authentication. A non-loopback bind
requires `DIFFMIND_AUTH_TOKEN` or `DIFFMIND_TRUSTED_PROXY_SECRET`; overriding that
guard requires the explicit `--allow-unauthenticated` flag. Middleware protects
the SPA, APIs, event streams, and MCP; `/healthz` remains public. Trusted-proxy
identities carry viewer/editor/admin roles; mutations are audited.
The exact project GitHub webhook POST route has separate raw-body HMAC
authentication and never grants access to other APIs. `/metrics` remains behind
normal authentication. See [operations](operations.md).

The rootless application container has no Linux capabilities and keeps durable
writes in `/data`. TLS and OIDC login belong at the reverse proxy; Diffmind
validates the proxy secret and enforces the resulting user's role.

`DIFFMIND_PROJECT_ACCESS=scoped` adds explicit per-project viewer/editor grants.
Independent [agent tokens](agent-tokens.md) can grant the same one-project roles
without proxy identity. Only global admins issue/revoke them; expiry and
revocation are rechecked from their hash-only registry. They are disabled in
legacy mode and never fall back to a stronger identity on authentication failure.
The default `legacy` mode keeps global roles. Route-level checks resolve project
ownership before handlers read data; query-service policies filter discovery
and enforce explicit/default project selection. Scoped remote MCP creates fresh
stateless per-request servers to avoid cached identity privileges. Access policies
use atomic writes with optimistic revisions. Configuration/imports remain
admin-only in scoped mode because all projects share the host and analyzer
credentials. Global admins, shared server tokens, OS users and local stdio MCP
retain trusted access. See [project access](project-access.md) for the exact
boundary and revocation limitations.

## Knowledge precedence

Identity is assembled from a repository-owned `.diffmind/service.yaml` override,
then matching project/globally installed packs by explicit priority, then the
repository name as fallback. Equal-priority conflicting service identities fail
instead of silently choosing one. Graph runs record `knowledge_pack_set_digest`
for the exact pack content used.

## Snapshot queries

The comparison screen, versioned HTTP endpoints, and MCP tools share
`internal/workspace/query`. Comparison projects persisted architecture graphs
into semantic facts in `internal/workspace/archgraph`; it does not reanalyze
source or modify run artifacts. Both run IDs are mandatory. Directed path
queries retain saved edge evidence and report incomplete searches explicitly.
Object traces only attach exact-ID local connections and dependency details;
they do not infer execution continuity from neighboring services. See
[graph history and tracing](graph-history.md) for the contract and limits.
