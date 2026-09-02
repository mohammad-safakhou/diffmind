# Company deployment

DiffMind can run as one continuously refreshed architecture service for a team.
The same process hosts the web graph, stable query API, and streamable HTTP MCP
endpoint; all of them read the same persisted graph artifacts.

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
host before upgrades and restore it as a unit.

## Authentication and TLS

`DIFFMIND_AUTH_TOKEN` is mandatory in the Compose deployment. It protects the
UI, APIs, server-sent events, and `/mcp`. Only `/healthz` is public.

- Browsers use HTTP Basic authentication: any username, token as password.
- API and MCP clients use `Authorization: Bearer <token>`.
- `X-DiffMind-Token` is also accepted for simple internal integrations.

The built-in server does not terminate TLS and the token represents the whole
deployment. For anything beyond localhost, place DiffMind behind HTTPS. Use an
identity-aware proxy or ingress as well when the organization needs per-user
access control and audit logs. Never put the token in a repository, image, URL,
or command history.

## Repository credentials

Set `GITHUB_TOKEN` (or `GH_TOKEN`) in the container environment to import and
refresh private GitHub repositories over HTTPS. Give it read-only access to the
smallest repository set possible. Prefer your platform's secret store instead
of committing a value to `.env`.

The service refuses to overwrite a dirty managed checkout. A failed sync is
recorded in refresh status and the existing checkout remains available for
inspection.

## Continuous refresh

These environment variables control fleet refresh:

| Variable | Default | Meaning |
| --- | --- | --- |
| `DIFFMIND_REFRESH_ON_START` | `true` | Refresh once after server startup. |
| `DIFFMIND_REFRESH_INTERVAL` | `15m` | Interval between fleet refreshes; empty or `0` disables the schedule. |
| `DIFFMIND_REFRESH_CONCURRENCY` | `4` | Parallel repository sync/analysis work, capped at 16. |

Each refresh syncs every managed Git checkout to its configured remote branch,
analyzes service repositories, and starts one new graph run per non-empty
project. The scheduler never overlaps runs.

Inspect or trigger it manually:

```bash
curl -H "Authorization: Bearer $DIFFMIND_AUTH_TOKEN" \
  https://diffmind.example.com/api/v1/refresh/status

curl -X POST -H "Authorization: Bearer $DIFFMIND_AUTH_TOKEN" \
  https://diffmind.example.com/api/v1/refresh
```

The manual endpoint returns `202 Accepted`; it returns `409 Conflict` while a
refresh is already running.

## Connect developer agents

The remote endpoint is `https://<host>/mcp`. Any streamable HTTP MCP client can
use it with a bearer token. Codex configuration is:

```bash
export DIFFMIND_AUTH_TOKEN='<the server token>'
codex mcp add diffmind \
  --url https://diffmind.example.com/mcp \
  --bearer-token-env-var DIFFMIND_AUTH_TOKEN
```

The exposed tools are read-only: project and service discovery, graph summary,
dependency traversal, architecture search, and deterministic impact analysis.
The refresh and mutation APIs are deliberately not MCP tools.

## Direct binary deployment

The same mode is available without containers:

```bash
DIFFMIND_AUTH_TOKEN='<token>' \
DIFFMIND_REFRESH_ON_START=true \
DIFFMIND_REFRESH_INTERVAL=15m \
diffmind ui --host 0.0.0.0 --port 8090
```

A non-loopback bind without a token fails closed. The escape hatch
`--allow-unauthenticated` exists only for a trusted external authentication
proxy that makes the application listener unreachable directly.
