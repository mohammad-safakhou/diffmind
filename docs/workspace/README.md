# Workspace guide

The workspace is part of the single DiffMind repository and binary. It owns
projects, repository imports, analysis scheduling, graph construction, the
versioned query API, MCP access, and the web interface.

Start it from an installed release:

```bash
diffmind doctor
diffmind
```

Or from a source checkout:

```bash
make build
./bin/diffmind
```

Open `http://127.0.0.1:8090`, create a project, and choose **Import & build**.
The guided operation discovers a GitHub organization or local repository tree,
imports and syncs it, runs deterministic analysis, and builds the project graph.
Progress is persisted under the project, so reloads show the current phase and
the last result. The individual import, analysis, and graph actions remain
available for advanced workflows. All state defaults to `~/.diffmind`; set
`DIFFMIND_HOME` to relocate it.

For the complete first-use and agent integration flow, see
[the developer workflow](docs/SYSTEM_HANDOFF.md). For implementation details,
see [the architecture guide](../ARCHITECTURE.md).

## Ingestion API

The web workflow uses a project-scoped asynchronous endpoint:

```text
POST /api/projects/{project}/ingestion
GET  /api/projects/{project}/ingestion
```

The POST body may contain an `import` object using the same GitHub/local fields
as repository import, plus `concurrency` and deterministic analysis `options`.
Omit `import` to rebuild all repositories already registered in the project.
POST returns `202 Accepted`; GET reports `running`, `completed`, `partial`, or
`failed`, the current phase, counters, graph run ID, and errors. A project
ingestion is exclusive with fleet refresh and conflicting project mutations.
