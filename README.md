# DiffMind

DiffMind turns a collection of source repositories into an explorable software
architecture map. It deterministically extracts endpoints, dependencies,
queues, data stores, scheduled work, and source-level flows, then connects the
results across services in a local web workspace.

Everything lives in this repository and ships through one `diffmind` command.

## Quick start

Release install on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/mohammad-safakhou/diffmind/master/install.sh | sh
diffmind doctor
diffmind
```

The installer verifies the release checksum before installing. Set
`DIFFMIND_INSTALL_DIR` to choose a destination or `DIFFMIND_VERSION` to pin a
version.

Install with Go instead:

```bash
go install github.com/mohammad-safakhou/diffmind/cmd/diffmind@latest
diffmind doctor
diffmind
```

Homebrew recipes are included in this same repository. See
[distribution](docs/distribution.md) for development tap setup and pinned release
generation/availability; no second hosted repository is required.

Build from source. Requirements:

- Go 1.26.6 or newer (see `go.mod`)
- Node.js 24 when rebuilding the web interfaces (the CI version)
- Git
- A C compiler for tree-sitter/CGO (also required by `go install`)
- Docker only for optional containerized SCIP indexing or shared deployment

```bash
git clone https://github.com/mohammad-safakhou/diffmind.git
cd diffmind
make build
./bin/diffmind
```

Open `http://127.0.0.1:8090`, create a project, then choose **Import & build**.
Point DiffMind at a GitHub organization or a directory containing local Git
repositories. One durable operation imports and syncs the repositories, runs
deterministic analysis, and builds the project graph. Its progress survives page
reloads and failures remain visible with actionable error details.

Use **Update graph** for incremental refresh, **Cancel ingestion** to stop work,
and **Resume / retry** to recover failed or cancelled jobs. Interrupted jobs
resume on server startup; unchanged successful analyses are reused after their
inputs and artifacts are verified. See [ingestion operations](docs/ingestion.md).

Start with the [current architecture](docs/ARCHITECTURE.md) and
[product roadmap](docs/ROADMAP.md) when contributing or evaluating the platform.

## Commands

```text
diffmind                         Open the web workspace
diffmind ui                      Open the web workspace
diffmind run --repo <path>       Analyze one repository
diffmind validate --run <id>     Validate an analysis run
diffmind list-runs               List analysis runs
diffmind graph --project <id>    Build a project graph
diffmind list projects           List projects
diffmind list runs --project ID  List project graph runs
diffmind extractor-ui            Open the low-level analysis dashboard
diffmind pack init <directory>   Scaffold a tested knowledge pack
diffmind pack lint <path>        Validate pack manifests and rules
diffmind pack test <path>        Execute synthetic pack fixtures
diffmind pack install <source>   Install and lock a local or Git pack
diffmind pack explain <path>     Explain what a pack derives from a repository
diffmind mcp [--project ID]      Run the read-only stdio MCP server
diffmind doctor [--json]         Check installation and graph readiness
diffmind version [--json]        Print release/build information
diffmind backup create --offline --output FILE  Save an offline snapshot
diffmind backup verify --archive FILE           Verify without extracting
diffmind backup restore --offline --archive FILE --destination NEW_DIR
```

DiffMind stores local state under `~/.diffmind` by default. Set
`DIFFMIND_HOME` to use another location.
See [backup/recovery](docs/backup-recovery.md) before restoring data and the
[synthetic quickstart](docs/contributor-quickstart.md) for a first contribution.

## Connect your coding agent

Build a graph in the web workspace first. Then register the stdio MCP server.
If you have multiple DiffMind projects, add `--project <project-id>` after
`diffmind mcp` in these examples.

Codex:

```bash
codex mcp add diffmind -- diffmind mcp
```

Claude Code:

```bash
claude mcp add diffmind -- diffmind mcp
```

Cursor project configuration (`.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "diffmind": {
      "command": "diffmind",
      "args": ["mcp"]
    }
  }
}
```

The MCP server offers read-only tools to list projects and services, inspect a
service and its evidence, search architecture objects, traverse dependencies,
calculate change impact, compare saved graph versions, and inspect exact-ID local
flows. See [graph history and tracing](docs/graph-history.md) for examples and
limits. It returns structured JSON and works entirely from
persisted deterministic graph artifacts—no model is used during analysis.

## Run it for a company

The included Compose deployment runs the UI, query API, scheduled repository
refresh, and streamable HTTP MCP endpoint as one rootless service with a
persistent volume:

```bash
cp .env.example .env
# Replace the placeholder token in .env; `openssl rand -hex 32` is suitable.
docker compose up -d
```

Open `http://localhost:8090`. The browser's authentication dialog accepts any
username and uses `DIFFMIND_AUTH_TOKEN` as the password. Put a TLS reverse proxy
in front of DiffMind before exposing it beyond a trusted machine or network.
Company deployments can pass authenticated users from an OIDC proxy with
viewer/editor/admin roles; DiffMind records all mutation attempts in a JSONL
audit log under its data directory.

Enable `DIFFMIND_PROJECT_ACCESS=scoped` to limit users to explicit project
memberships across UI, HTTP and remote MCP. Admins manage grants in **Project
access**; editors can refresh assigned projects. See [project permissions](docs/project-access.md)
for setup, recovery, and the distinction between per-user identities and the
shared **global-admin** token. The default `legacy` mode retains global roles.

For restricted agent access without proxy-issued credentials, admins can issue
an expiring **viewer token for one project** in **Project access → Agent tokens**.
Use it as a bearer credential for `/mcp` or the query API. See
[agent token setup, rotation, and revocation](docs/agent-tokens.md).

Remote API clients send `Authorization: Bearer <token>`. Remote MCP clients use
the `/mcp` endpoint. The following is an **admin connection**, not a scoped user
connection; restricted agents must authenticate through the identity proxy:

```bash
export DIFFMIND_AUTH_TOKEN='<the server token>'
codex mcp add diffmind \
  --url https://diffmind.example.com/mcp \
  --bearer-token-env-var DIFFMIND_AUTH_TOKEN
```

By default the service refreshes every registered Git repository on startup and
every 15 minutes, re-analyzes it, and publishes a new project graph. Set
`GITHUB_TOKEN` for private GitHub repositories. See
[company deployment](docs/company-deployment.md) for operations, security, and
backup details.

Projects also have an **Operations** screen for queued refreshes, cancellation,
retry, and durable job/ingestion history. Opt-in signed GitHub push webhooks,
bounded workers, and authenticated metrics are described in
[continuous operations](docs/operations.md).

For large job histories, [migrate the refresh queue to indexed SQLite](docs/queue-storage.md).
The offline migration preserves original job records, attempts and timestamps;
no external database service is needed. This still runs as one server, not a
distributed worker deployment.

## Query API

While the web workspace is running, integrations can query the same graph core
under `/api/v1`:

```text
GET /api/v1/projects
GET /api/v1/projects/{project}/graph/summary
GET /api/v1/projects/{project}/graph/runs?offset=0&limit=100
GET /api/v1/projects/{project}/graph/compare?from=RUN_A&to=RUN_B
GET /api/v1/projects/{project}/graph/path?from=SERVICE_A&to=SERVICE_B&depth=6
GET /api/v1/projects/{project}/graph/trace?service=SERVICE&object_id=OBJECT_ID
GET /api/v1/projects/{project}/services
GET /api/v1/projects/{project}/services/{service}
GET /api/v1/projects/{project}/dependencies?service=...&direction=both
GET /api/v1/projects/{project}/impact?target=...&depth=6
GET /api/v1/projects/{project}/search?q=...&limit=50
```

Single-graph queries accept `run=<completed-run-id>` to pin a historical graph;
otherwise the latest completed graph is used. Comparisons require explicit
`from` and `to` run IDs. In the project workspace, select **Compare graphs** to
browse before/after facts and their evidence.

On an authenticated shared server, add `Authorization: Bearer <token>` to every
API request. `GET /healthz` remains unauthenticated for health checks.

## How it works

```text
source repositories
        │
        ▼
deterministic extraction
        │
        ▼
DiffMind service documents
        │
        ▼
cross-service resolution and graph
        │
        ▼
local web workspace
```

The canonical service document is written to
`.diffmind/context/service.json` inside each run. Its schema identifier is
`diffmind.service.v1`.

## Repository layout

```text
cmd/diffmind/              unified command
internal/extractor/        repository analysis engine
internal/workspace/        projects, graph construction, API, and web UI
protocol/                  shared service-document model and validation
indexerbuild/              reproducible SCIP indexer image
packs/                     official, tested knowledge packs
testdata/                  public synthetic fixtures
docs/                      design and configuration references
```

## Development

```bash
make test
make test-packs
make test-race
make ui-build
make ui-test
make build
```

External SCIP integration tests are opt-in because they require language
indexers and their toolchains:

```bash
DIFFMIND_RUN_SCIP_INTEGRATION=1 go test ./internal/extractor/scip
```

See [the architecture guide](docs/ARCHITECTURE.md) and
[knowledge-pack guide](docs/knowledge-packs.md) for more.

Teach configuration conventions without an LLM using evidence-backed HTTP/RPC
and queue rules. `diffmind pack init` scaffolds exact multi-repository graph tests;
the opt-in [service-manifest](packs/service-manifest/README.md) and
[OpenFeign configuration](packs/spring-openfeign-config/README.md) packs provide
working examples. Check the [tested support matrix](docs/supported-patterns.md)
for coverage and limits.

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md)
and [SECURITY.md](SECURITY.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
