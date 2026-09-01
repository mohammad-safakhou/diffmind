# DiffMind

DiffMind turns a collection of source repositories into an explorable software
architecture map. It deterministically extracts endpoints, dependencies,
queues, data stores, scheduled work, and source-level flows, then connects the
results across services in a local web workspace.

Everything lives in this repository and ships through one `diffmind` command.

## Quick start

Requirements:

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

Install directly with Go:

```bash
go install github.com/mohammad-safakhou/diffmind/cmd/diffmind@latest
diffmind
```

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
```

DiffMind stores local state under `~/.diffmind` by default. Set
`DIFFMIND_HOME` to use another location.

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
