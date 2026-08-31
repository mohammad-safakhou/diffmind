# DiffMind

DiffMind is the project workspace and graph orchestrator for DiffMind.

Use DiffMind when you want to:

- create a project for a company/team/service group,
- add local repositories or import many repositories,
- run deterministic DiffMind on selected repositories,
- build a cross-service graph from DiffMind protocol outputs,
- explore services, teams, resources, dependencies, and flows.

DiffMind does not extract source code itself. It launches DiffMind and consumes
the generated DiffMind protocol service documents.

## Repository Layout

The development checkout expects these sibling repositories:

```text
eyes/
  protocol/
  diffmind/
  diffmind/
```

`diffmind/go.mod` uses:

```text
replace github.com/mohammad-safakhou/protocol => ../protocol
```

## Requirements

- Go 1.25.0 or newer compatible toolchain.
- Node.js and npm for the web UI build.
- DiffMind available from the sibling `../diffmind` checkout or installed as a
  binary in `PATH`.
- Git for cloning/syncing repositories.
- Optional: GitHub CLI `gh` authenticated with SSO if importing private GitHub
  repositories.

## Build And Test

```bash
cd /path/to/eyes/diffmind
go test ./...

cd internal/ui/web
npm install
npm run build

cd /path/to/eyes/diffmind
go build -o ./bin/diffmind ./cmd/diffmind
```

## Start The UI

Development:

```bash
cd /path/to/eyes/diffmind
go run ./cmd/diffmind ui --host 127.0.0.1 --port 8090
```

Built binary:

```bash
./bin/diffmind ui --host 127.0.0.1 --port 8090
```

Open:

```text
http://127.0.0.1:8090
```

Useful flags:

| Flag | Meaning |
| --- | --- |
| `--host` | UI/API bind host. Default `127.0.0.1`. |
| `--port` | UI/API port. Default `8090`. |
| `--log-level` | `info`, `debug`, or `trace`. |
| `--no-spa-rebuild` | Serve existing UI bundle without auto rebuilding. |

## Data Locations

DiffMind stores project metadata under:

```text
~/.diffmind/projects/<project_id>/
```

DiffMind stores run artifacts under:

```text
~/.diffmind/runs/<run_id>/
```

DiffMind graph runs point at DiffMind run IDs. If you delete DiffMind run
directories, older DiffMind graph runs may no longer be rebuildable.

## UI Workflow

1. Open DiffMind.
2. Click `New Project`.
3. Add repositories:
   - `Add repository` for one local or Git repository.
   - `Import repositories` for GitHub org or local directory scanning.
4. Select one repository and click `Configure run` to run DiffMind for one repo.
5. Or click `Run DiffMind on all repositories` for a batch run.
6. Wait for repository status to become `COMPLETED`.
7. Click `Build graph`.
8. Use the graph:
   - `Overview` for high-level service/resource map.
   - `Expand selected` for one service workspace.
   - Team filter for one team.
   - Team scope `Team only` or `Team + connected`.
   - Search to focus a service.
   - Click services, routes, dependencies, DBs, queues, caches, or edges for
     inspector details.

## Importing Repositories

### Local Directory Import

Use this when repositories are already cloned.

UI:

1. Click `Import repositories`.
2. Select `Local directory`.
3. Set `Root directory` to the folder containing repositories.
4. Enable `Recursive scan` if repositories are nested.
5. Set `Max depth`.
6. Optional: add include/exclude regex.
7. Click `Import repositories`.

Only directories containing `.git` are imported.

### GitHub Org Import

Use this when repositories should be listed and optionally cloned from GitHub.

Recommended setup for private GitHub or GitHub Enterprise with SSO:

```bash
gh auth login
gh auth status
```

For enterprise hosts:

```bash
gh auth login --hostname github.company.example
```

UI:

1. Click `Import repositories`.
2. Select `GitHub org`.
3. Enter org name.
4. Set API base:
   - public GitHub: leave empty or use `https://api.github.com`
   - GitHub Enterprise: `https://github.company.example/api/v3`
5. Choose clone transport:
   - `Auto` prefers HTTPS with token when available.
   - `SSH` uses SSH clone URLs.
   - `HTTPS` uses HTTPS clone URLs.
6. Set clone concurrency.
7. Optional: include/exclude regex.
8. Click `Import repositories`.

If SSO blocks access, authorize the `gh` token in GitHub SSO and retry.

## Running DiffMind From DiffMind

DiffMind starts DiffMind deterministically. There is no LLM mode.

Run options in the UI map to DiffMind CLI flags:

| UI option | DiffMind flag |
| --- | --- |
| Workers | `--workers` |
| Min confidence | `--min-confidence` |
| Config JSON | `--config` |
| Output directory | `--out` |
| Log file | `--log-file` |
| Verbose | `--verbose` |
| Trace | `--trace` |

For most repos, leave config JSON empty and use repo-local
`diffmind-configuration.yaml` for hints.

## Build A Graph From CLI

After repositories have DiffMind run IDs:

```bash
cd /path/to/eyes/diffmind
go run ./cmd/diffmind graph --project <project_id>
```

List projects:

```bash
go run ./cmd/diffmind list projects
```

List graph runs:

```bash
go run ./cmd/diffmind list runs --project <project_id>
```

Build graph from explicit DiffMind runs:

```bash
go run ./cmd/diffmind graph \
  --project <project_id> \
  --repo orders-api=<diffmind_run_id> \
  --repo payments-api=<diffmind_run_id>
```

## Reading The Graph

DiffMind graph nodes:

| Node | Meaning |
| --- | --- |
| Service | A repository/service with DiffMind protocol output. |
| External service | A dependency target not registered as a project service. |
| Database/cache | Resource used by one or more services. |
| Queue/topic/stream | Event resource. |
| Scheduler/cron | Activation that triggers a service. |

Large graph layout rules:

- Teams are grouped.
- Connected teams are placed closer than unrelated teams.
- Single-service resources attach near that service.
- Shared resources sit near the services that use them.
- Schedulers attach to the left side of the service they trigger.

## Troubleshooting

Project opens slowly

: DiffMind loads workspace metadata first and graph data separately. Large graphs
  can still take time to render in the browser. Use team filters and search.

`json: cannot unmarshal object into Go value of type []ui.githubRepo`

: GitHub returned an error object instead of a repository list. Check org name,
  API base, token, SSO authorization, and GitHub Enterprise URL.

GitHub import finds no repos

: Run `gh auth status`. For SSO, authorize the token for the org. For enterprise,
  use the correct API base: `https://<host>/api/v3`.

DiffMind run fails with module/path errors

: Ensure the `diffmind` checkout is next to `diffmind`, or install `diffmind` in
  `PATH`. The development setup assumes sibling checkouts under `eyes/`.

Graph has unresolved external services

: Add service aliases or `http_targets` in the relevant repository's
  `diffmind-configuration.yaml`, rerun DiffMind for that repo, then rebuild the
  graph.

Graph has too many resource nodes

: Add `aliases.resources` or `resource_patterns` to collapse company-specific
  names to canonical DB/cache/queue identities.

Dirty working tree warning

: A DiffMind run was produced from a repository with uncommitted source/config
  changes. Commit/stash them and rerun if reproducibility matters.

## Related Docs

- DiffMind: `../diffmind/README.md`
- DiffMind configuration: `../diffmind/docs/CONFIGURATION.md`
- DiffMind protocol: `../protocol/README.md`
