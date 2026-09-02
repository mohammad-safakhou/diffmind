# Developer workflow

DiffMind is one repository and one command. It analyzes source repositories
without an LLM, builds a cross-service architecture graph, serves that graph in
a browser, and exposes the same read-only query surface to coding agents over
MCP.

## Install and verify

macOS or Linux release:

```bash
curl -fsSL https://raw.githubusercontent.com/mohammad-safakhou/diffmind/master/install.sh | sh
diffmind doctor
```

Source installation:

```bash
go install github.com/mohammad-safakhou/diffmind/cmd/diffmind@latest
diffmind doctor
```

Docker is optional but recommended for containerized SCIP indexing. Git is
required. DiffMind keeps its state under `~/.diffmind` unless `DIFFMIND_HOME`
is set.

## Build a company graph

1. Run `diffmind` and open `http://127.0.0.1:8090`.
2. Create a project for a system, product area, or organization.
3. Choose **Import & build**, then scan a directory of Git repositories or
   import a GitHub organization. DiffMind imports, syncs, analyzes, and builds
   the graph as one durable operation. Reloading the page does not lose its
   progress or errors.
4. Explore services, teams, entrypoints, data stores, queues, traces, and impact
   from the project workspace.

The analysis is deterministic. Company-specific conventions belong in a
Knowledge Pack. Open `Knowledge packs` in the project workspace or use
`diffmind pack init`, `lint`, `test`, `explain`, and `install`. Packs are
versioned, fixture-tested, checksummed, and safe to contribute without exposing
company source code.

## Connect a coding agent

The MCP server communicates over stdio and writes no logs to stdout:

```bash
diffmind mcp --project <project-id>
```

An MCP client can launch that command with no network transport. If there is
only one DiffMind project, `--project` can be omitted. A shared deployment also
serves streamable HTTP MCP at `/mcp`, protected by the server bearer token. The
server exposes:

- `list_projects`
- `get_graph_summary`
- `list_services`
- `get_service`
- `get_dependencies`
- `search_architecture`
- `get_impact`

All tools are read-only and return both structured JSON and text-compatible MCP
content. Agents can therefore answer questions such as “what calls catalog?”,
“which services are affected by changing this queue?”, and “where is this
endpoint implemented?” without re-reading every repository.

## Query without MCP

The local server exposes stable read-only endpoints under `/api/v1`:

```text
GET /api/v1/projects
GET /api/v1/projects/{project}/graph/summary
GET /api/v1/projects/{project}/services
GET /api/v1/projects/{project}/services/{service}
GET /api/v1/projects/{project}/dependencies?service=...&direction=both
GET /api/v1/projects/{project}/impact?target=...&depth=6
GET /api/v1/projects/{project}/search?q=...&limit=50
```

Pass `run=<completed-run-id>` to pin a historical graph; otherwise queries use
the latest completed graph with a persisted `graph.json` artifact.

## Improve extraction safely

Start without custom rules. If a service or resource is unresolved, create a
Knowledge Pack with synthetic fixtures that model the convention. Run:

```bash
diffmind pack lint ./path/to/pack
diffmind pack test ./path/to/pack
diffmind pack explain ./path/to/pack --repo ./synthetic-or-local-repo
```

Contribute generally useful packs with synthetic data and an open-source
license. Never contribute proprietary source, credentials, internal hostnames,
or organization names.

## Company-wide operation

Use `compose.yaml` for a shared installation. It requires an authentication
token, stores all projects and run artifacts on a persistent volume, refreshes
registered Git repositories on startup and at a configurable interval, and
serves both the web/API and remote MCP surfaces. Refresh status and manual
triggering are available at `GET /api/v1/refresh/status` and
`POST /api/v1/refresh`.

The built-in token is a deployment-wide admin recovery credential. For normal
company access, put the service behind TLS and a trusted identity proxy that
sets DiffMind's `viewer`, `editor`, or `admin` role headers. State-changing
requests are recorded in the audit log. Back up the `/data` volume and inject
repository credentials as runtime secrets. See
[company deployment](../../company-deployment.md).
