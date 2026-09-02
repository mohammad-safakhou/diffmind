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

Build from source. Requirements:

- Go 1.26.2 or newer
- Node.js 20 or newer when rebuilding the web interfaces
- Git
- Docker for containerized SCIP indexing

```bash
git clone https://github.com/mohammad-safakhou/diffmind.git
cd diffmind
make build
./bin/diffmind
```

Open `http://127.0.0.1:8090`, create a project, add repositories, run DiffMind,
and build the project graph.

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
```

DiffMind stores local state under `~/.diffmind` by default. Set
`DIFFMIND_HOME` to use another location.

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
and calculate change impact. It returns structured JSON and works entirely from
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

Remote API clients send `Authorization: Bearer <token>`. Remote MCP clients use
the `/mcp` endpoint. For example, Codex can connect directly to a shared host:

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

## Query API

While the web workspace is running, integrations can query the same graph core
under `/api/v1`:

```text
GET /api/v1/projects
GET /api/v1/projects/{project}/graph/summary
GET /api/v1/projects/{project}/services
GET /api/v1/projects/{project}/services/{service}
GET /api/v1/projects/{project}/dependencies?service=...&direction=both
GET /api/v1/projects/{project}/impact?target=...&depth=6
GET /api/v1/projects/{project}/search?q=...&limit=50
```

Add `run=<completed-run-id>` to pin a historical graph; otherwise the latest
completed graph is used.

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

## Contributing

Issues and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md)
and [SECURITY.md](SECURITY.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).
