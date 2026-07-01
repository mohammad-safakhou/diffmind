# Eyes System Handoff Guide

This guide is for an engineer who has never used the system and wants to test it
against company services.

The system has three repositories:

```text
protocol      shared service-context protocol
diffmind  deterministic extractor for one service repository
diffmind   project UI, DiffMind orchestration, and company graph
```

Use DiffMind first. It will run DiffMind for you.

## 1. Clone The Repositories

Recommended local layout:

```bash
mkdir -p ~/repos/eyes
cd ~/repos/eyes

git clone <protocol-repo-url> protocol
git clone <diffmind-repo-url> diffmind
git clone <diffmind-repo-url> diffmind
```

The sibling layout matters because DiffMind and DiffMind currently use local
module replacements:

```text
replace github.com/mohammad-safakhou/protocol => ../protocol
```

## 2. Install Prerequisites

Required:

```text
Go compatible with:
  protocol:    go 1.25.0
  diffmind: go 1.25.0
  diffmind: go 1.26.2

Node.js + npm
Git
```

Recommended:

```text
Docker
GitHub CLI (gh), if importing private GitHub repositories
```

For private GitHub with SSO:

```bash
gh auth login
gh auth status
```

For GitHub Enterprise:

```bash
gh auth login --hostname github.company.example
```

Authorize the `gh` token in your company SSO if GitHub asks for it.

## 3. Verify The Checkout

```bash
cd ~/repos/eyes/protocol
go test ./...

cd ~/repos/eyes/diffmind
go test ./...

cd ~/repos/eyes/diffmind
go test ./...

cd ~/repos/eyes/diffmind/internal/ui/web
npm install
npm run build
```

## 4. Start DiffMind

```bash
cd ~/repos/eyes/diffmind
go run ./cmd/diffmind ui --host 127.0.0.1 --port 8090
```

Open:

```text
http://127.0.0.1:8090
```

Do not run DiffMind UI first. DiffMind is the main workflow.

## 5. Create A Project

In the UI:

1. Click `New Project`.
2. Name it after the company/team/domain you want to inspect.
3. Open the project.

DiffMind stores project metadata under:

```text
~/.diffmind/projects/<project_id>/
```

## 6. Add Repositories

### Option A: Add One Local Repository

Use this for first testing.

1. Click `Add repository`.
2. Choose `Local`.
3. Enter the absolute repository path.
4. Set team name if known.
5. Click `Add`.

### Option B: Import A Local Directory

Use this when repositories are already cloned.

1. Click `Import repositories`.
2. Choose `Local directory`.
3. Enter the root folder containing service repositories.
4. Enable `Recursive scan` if repositories are nested.
5. Set `Max depth`.
6. Optionally set include/exclude regex.
7. Click `Import repositories`.

Only directories containing `.git` are imported.

### Option C: Import GitHub Organization

Use this when repositories should be listed or cloned from GitHub.

1. Click `Import repositories`.
2. Choose `GitHub org`.
3. Enter organization name.
4. API base:
   - GitHub.com: leave empty or use `https://api.github.com`
   - GitHub Enterprise: `https://github.company.example/api/v3`
5. Choose clone transport:
   - `Auto`: use token/HTTPS when available.
   - `SSH`: use SSH clone URLs.
   - `HTTPS`: use HTTPS clone URLs.
6. Set concurrency.
7. Optional: include/exclude regex.
8. Click `Import repositories`.

If you see GitHub JSON errors, verify org name, API base, token, and SSO
authorization.

## 7. Add Repository Hints Only When Needed

Each service repository may optionally contain:

```text
diffmind-configuration.yaml
```

Start without it. Add it only when you need:

- service/team/domain override,
- service aliases,
- database/cache/queue aliases,
- HTTP target resolution,
- custom config paths,
- dependency injection/wiring conventions,
- narrow custom regex patterns.

Minimal example:

```yaml
schema: diffmind.config.v1

service:
  id: orders-api
  name: orders-api
  team: commerce

aliases:
  services:
    payments-api:
      - payments
      - payments-api.default.svc.cluster.local

http_targets:
  - id: payments-client
    service_ref: service.payments-api
    client_class: PaymentsClient
    config_key: services.payments.url
```

Full reference:

```text
../diffmind/docs/CONFIGURATION.md
```

## 8. Run DiffMind

In DiffMind:

1. Select one repository.
2. Click `Configure run`.
3. Leave pipeline as deterministic. There is no LLM mode.
4. Recommended first-run options:
   - workers: `8`
   - min confidence: `0.70`
   - verbose: off
   - trace: off
5. Click `Start run`.

For a full project:

1. Click `Run DiffMind on all repositories`.
2. Set batch concurrency.
3. Keep `Skip fresh repositories` enabled after the first full run.
4. Click `Start batch`.

DiffMind writes artifacts under:

```text
~/.diffmind/runs/<run_id>/
```

The important file is:

```text
~/.diffmind/runs/<run_id>/.diffmind/context/service.json
```

## 9. Build The Graph

After repositories have completed DiffMind runs:

1. Click `Build graph`.
2. Wait for the graph build status to complete.
3. Open the graph workspace.

DiffMind stores graph run metadata under the project directory.

CLI equivalent:

```bash
cd ~/repos/eyes/diffmind
go run ./cmd/diffmind graph --project <project_id>
```

## 10. Use The Graph

Recommended workflow:

1. Start in `Overview`.
2. Pick a team with the team selector.
3. Use `Team only` to reduce noise.
4. Use `Team + connected` to include immediate cross-team dependencies.
5. Search for a service if the graph is large.
6. Click a service.
7. Use `Expand selected`.
8. Click an entrypoint or dependency row.
9. Inspect:
   - request/response details,
   - target service/resource,
   - DB query/table details,
   - evidence and observations,
   - local flow/sequence when extracted.

Graph node meanings:

| Node | Meaning |
| --- | --- |
| Service | A registered project service. |
| External service | Target service not registered in the project. |
| Database/cache | Resource used by services. |
| Queue/topic/stream | Event resource. |
| Scheduler/cron | Activation that triggers a service. |

## 11. Expected Output Quality

After a good run, you should see:

- every service registered as a service node,
- routes/RPC endpoints/queues/CLI/schedulers as entrypoints,
- HTTP/RPC calls as dependencies,
- DB/cache/queue operations grouped under resources,
- shared resources placed between related services,
- external services shown clearly when not in the project,
- object details available in the inspector.

Warnings are not always failures, but they are work items:

| Warning | Meaning |
| --- | --- |
| unresolved external target | Add aliases or `http_targets`. |
| missing evidence | Detector needs better evidence or config-derived marking. |
| dirty working tree | Repository had uncommitted source/config changes. |
| operation labels suppressed | Add target aliases so operation names are not treated as services. |

## 12. Troubleshooting

DiffMind project is slow to open

: Large projects load metadata first and graph separately. Use team filters and
  search. For hundreds of services, avoid full-detail mode until focused on a
  team/service.

DiffMind finds nothing

: Check language/framework support. Run one repository directly with
  `--verbose`. Add config paths or DI conventions if the service is heavily
  custom-wired.

Outbound target is unresolved

: Add `aliases.services` or `http_targets` in `diffmind-configuration.yaml`,
  rerun DiffMind for that repo, rebuild graph.

Database/cache/queue resources are duplicated

: Add `aliases.resources` or `resource_patterns`, rerun DiffMind, rebuild graph.

GitHub import fails with JSON unmarshal error

: GitHub returned an error object. Check token, SSO, API base, org name, and
  rate limit.

Run failed with `runtime.pipeline=llm`

: Remove old config. DiffMind is deterministic-only.

Run failed with `unknown detector`

: Remove old detector IDs from config. DiffMind runs detectors automatically.

## 13. What To Send Back When Testing A New Company

Ask the engineer to report:

- project name,
- number of repositories imported,
- number of successful DiffMind runs,
- failed run IDs and `run_failure.md`,
- graph run ID,
- unresolved service warnings,
- duplicated resources,
- one or two services where important dependencies are missing,
- example source files for missing custom framework/wiring patterns.

That gives enough information to add general detectors or clean configuration
rules without overfitting to one repository.
