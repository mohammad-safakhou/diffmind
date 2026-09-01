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

Open `http://127.0.0.1:8090`, create a project, add or import repositories,
run deterministic analysis, and build the project graph. All state defaults to
`~/.diffmind`; set `DIFFMIND_HOME` to relocate it.

For the complete first-use and agent integration flow, see
[the developer workflow](docs/SYSTEM_HANDOFF.md). For implementation details,
see [the architecture guide](../ARCHITECTURE.md).
