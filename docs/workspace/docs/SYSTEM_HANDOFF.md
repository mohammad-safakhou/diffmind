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
3. Add one local repository, scan a directory of Git repositories, or import
   repositories from GitHub.
4. Run DiffMind for one repository to validate the result, then use the batch
   action for the rest.
5. Build the graph after repository analysis completes.
6. Explore services, teams, entrypoints, data stores, queues, traces, and impact
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

An MCP client should launch that command with no network transport. If there is
only one DiffMind project, `--project` can be omitted. The server exposes:

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

The current milestone is local-first: a shared host can bind the HTTP server to
an internal interface and place `DIFFMIND_HOME` on persistent storage, but
authentication and scheduled refresh are not yet built in. Do not expose it to
an untrusted network. A production multi-user deployment should add an auth
proxy, repository credentials management, scheduled incremental refresh, and a
backed-up persistent volume.
