# Company deployment

DiffMind can run as one continuously refreshed architecture service for a team.
The same process hosts the web graph, stable query API, and streamable HTTP MCP
endpoint; all of them read the same persisted graph artifacts.

Refreshes now reuse unchanged, verified repository analyses. Interrupted UI
ingestions resume at server startup; explicit cancellations remain stopped.
See [ingestion and recovery](ingestion.md) for checkpoint semantics and limits.
Only one server process may write a given data volume.

Current CLI servers enforce a local singleton lock before recovery. For large
retained job histories, opt into [SQLite queue storage](queue-storage.md) using
the offline migration. This does not enable multiple replicas or remote workers.

Global admins can set each project's pending-job and repository-operation caps
from **Operations → Project resource limits**. Existing projects inherit the
server's global budgets. [Project limits](operations.md#per-project-resource-limits)
take effect without restart; reductions drain active work and preserve history.

## Start the service

Requirements are Docker Engine with Compose and enough disk for repository
clones plus analysis history.

```bash
cp .env.example .env
openssl rand -hex 32
# Put the generated value in .env as DIFFMIND_AUTH_TOKEN, then:
docker compose up -d
docker compose ps
```

The Compose file pulls the published `ghcr.io/mohammad-safakhou/diffmind:latest`
image when available and can build the same image from the checkout. Use
`docker compose up -d --build` to force a local build. Pin `DIFFMIND_IMAGE` to a
version or immutable `sha-*` image tag for controlled upgrades.

All state is under `/data` in the `diffmind-data` volume. This includes project
registrations, managed Git clones, extraction artifacts, knowledge packs, and
graph history. Back up that volume using the storage mechanism for your Docker
host before upgrades and restore it as a unit. Versioned offline
[backup/verify/restore commands](backup-recovery.md) are also available; that guide
covers credentials, maintenance locks, recovery drills, and volume-mount limits.

## Authentication, roles, and TLS

`DIFFMIND_AUTH_TOKEN` is mandatory in the Compose deployment. It protects the
UI, APIs, server-sent events, and `/mcp`. Only `/healthz` is public.

- Browsers use HTTP Basic authentication: any username, token as password.
- API and MCP clients use `Authorization: Bearer <token>`.
- `X-DiffMind-Token` is also accepted for simple internal integrations.

The shared token receives the `admin` role and is useful as a recovery credential.
For normal company access, put DiffMind behind an OIDC-capable reverse proxy or
ingress and set a separate `DIFFMIND_TRUSTED_PROXY_SECRET`. After authenticating
the user, the proxy must strip any client-supplied `X-DiffMind-*` headers and set:

| Header | Value |
| --- | --- |
| `X-DiffMind-Proxy-Secret` | Exact `DIFFMIND_TRUSTED_PROXY_SECRET` value. |
| `X-DiffMind-User` | Stable user ID or company email from the identity provider. |
| `X-DiffMind-Role` | `viewer`, `editor`, or `admin`; omitted means `viewer`. |

The proxy secret is what makes the identity headers trustworthy. Keep the
application port unreachable except through that proxy, use HTTPS between every
untrusted network boundary, and never reuse the shared admin token as the proxy
secret. A request with identity headers but without the correct proxy secret is
rejected.

By default (`DIFFMIND_PROJECT_ACCESS=legacy`), roles apply globally:

| Role | Access |
| --- | --- |
| `viewer` | UI, query API, event streams, and read-only MCP tools. |
| `editor` | Viewer access plus project, repository, pack, configuration, sync, and run mutations. |
| `admin` | Editor access plus deletes, fleet-wide refresh, access/token administration, and project resource limits. |

For explicit project memberships, opt into `DIFFMIND_PROJECT_ACCESS=scoped`.
Admins manage viewer/editor grants in **Project access**, the proxy role remains
a ceiling, and projects without grants are admin-only. Scoped editors can
refresh assigned projects, but host configuration/imports/packs are admin-only.
See [project permissions](project-access.md) before enabling this mode.

In scoped mode, administrators can also issue single-project viewer/editor
[agent tokens](agent-tokens.md) from **Project access**. These credentials expire,
can be revoked, and work with HTTP/MCP bearer authentication without an identity
proxy issuing credentials to the agent. They are separate service grants, not
memberships: revoke them separately during user offboarding.

`GET /api/v1/session` returns the authenticated user, global role, authentication
method, and project-access mode. The built-in server does not terminate TLS. Never put either secret in a
repository, image, URL, or command history.

## Audit log

Every state-changing HTTP request is appended to
`$DIFFMIND_HOME/audit/http.jsonl` (`/data/audit/http.jsonl` in Compose), including
successful changes, authorization failures, and authentication failures. Each
event contains a UTC timestamp, request ID, actor, role, authentication method,
HTTP method, path, status, and client IP. Query strings, request bodies, and
credentials are never recorded. Read requests are intentionally omitted to keep
high-volume graph and MCP access from overwhelming the log.

Back up and ship this file with the rest of DiffMind state. Access logs at the
identity proxy remain the source for read-request auditing.

## Repository credentials

Set `GITHUB_TOKEN` (or `GH_TOKEN`) in the container environment to import and
refresh private GitHub repositories over HTTPS. Give it read-only access to the
smallest repository set possible. Prefer your platform's secret store instead
of committing a value to `.env`.

The service refuses to overwrite a dirty managed checkout. A failed sync is
recorded in refresh status and the existing checkout remains available for
inspection.

For initial onboarding, create a project in the UI and choose **Import & build**.
That operation imports a GitHub organization or local repository tree, performs
the first sync and deterministic analysis, and builds the initial graph. Its
latest state is stored in the project as `ingestion.json`; interrupted work is
recovered at startup as described in [ingestion](ingestion.md). In legacy mode,
editors can start ingestion. In scoped mode initial onboarding is admin-only;
project editors can subsequently queue refreshes and viewers can monitor them.

## Continuous refresh

These environment variables control fleet refresh:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DIFFMIND_REFRESH_ON_START` | `true` | Refresh once after server startup. |
| `DIFFMIND_REFRESH_INTERVAL` | `15m` | Interval between fleet refreshes; empty or `0` disables the schedule. |
| `DIFFMIND_REFRESH_CONCURRENCY` | `4` | Per-project repository work, capped at 16. |
| `DIFFMIND_JOB_WORKERS` | `2` | Concurrent queued project refreshes, maximum 16. |
| `DIFFMIND_QUEUE_CAPACITY` | `256` | Queued plus running refresh jobs, maximum 10000. |
| `DIFFMIND_REPOSITORY_WORKERS` | `4` | Global active sync/analyzer operations, maximum 32. |
| `DIFFMIND_WEBHOOK_SECRET` | empty | Opt-in GitHub push authentication; separate secret of at least 32 bytes. |

Each refresh syncs every managed Git checkout to its configured remote branch,
analyzes service repositories, and starts one new graph run per non-empty
project. Refresh requests share a durable queue; different projects may run in
parallel within configured limits, but one project's refreshes never overlap.
Existing active project work defers queued refreshes without using an attempt.

Inspect or trigger it manually:

```bash
curl -H "Authorization: Bearer $DIFFMIND_AUTH_TOKEN" \
  https://diffmind.example.com/api/v1/refresh/status

curl -X POST -H "Authorization: Bearer $DIFFMIND_AUTH_TOKEN" \
  https://diffmind.example.com/api/v1/refresh
```

The manual endpoint returns `202 Accepted`; it returns `409 Conflict` while a
refresh is already running.

Use **Operations** in a project for job and ingestion-attempt history, queued
refresh, cancellation, and retry. See [continuous operations](operations.md) for
webhook setup, queue API, metrics, recovery guarantees, and deployment limits.
No GitHub webhook or public proxy configuration is created automatically.

## Connect developer agents

The remote endpoint is `https://<host>/mcp`. Any streamable HTTP MCP client can
use it through the configured authentication layer. The shared-token example
below grants **global-admin access**, bypassing project memberships:

```bash
export DIFFMIND_AUTH_TOKEN='<the server token>'
codex mcp add diffmind \
  --url https://diffmind.example.com/mcp \
  --bearer-token-env-var DIFFMIND_AUTH_TOKEN
```

The exposed tools are read-only: project and service discovery, graph summary,
dependency traversal, architecture search, deterministic impact analysis,
snapshot comparison, and exact-ID local object tracing. Viewers can use all
these tools. See [graph history and tracing](graph-history.md).
The refresh and mutation APIs are deliberately not MCP tools.

For scoped agents, prefer a viewer [project token](agent-tokens.md), passed as a
bearer header to `/mcp`; keep the shared admin credential out of agent settings.
Alternatively authenticate each agent through the identity proxy, which must
validate its non-interactive user credential and inject trusted identity headers
on every request. Diffmind's project tokens are not per-user SSO tokens. Do not give agents the
proxy secret or the shared recovery token. See [agents and revocation](project-access.md#agents-and-revocation).

## Direct binary deployment

The same mode is available without containers:

```bash
DIFFMIND_AUTH_TOKEN='<token>' \
DIFFMIND_REFRESH_ON_START=true \
DIFFMIND_REFRESH_INTERVAL=15m \
diffmind ui --host 0.0.0.0 --port 8090
```

A non-loopback bind without a shared token or trusted-proxy secret fails closed.
The escape hatch `--allow-unauthenticated` is intended only for isolated test
environments; it assigns every caller the local `admin` identity.
