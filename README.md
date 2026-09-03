# DiffMind

DiffMind turns your source repositories into an explorable architecture graph
for you and your coding agent. Inspect services, endpoints, dependencies, queues
and data stores; follow source evidence; compare saved graphs; and ask questions
through MCP. Your agent can also install, configure and operate the workspace.
Analysis is deterministic: no LLM or model API
key is needed to build the graph.

One repository, one `diffmind` command. Run it privately on your laptop or as a
continuously refreshed, single-server workspace for a team.

**Release status:** the current implementation is available from source. A
public binary release has not yet been published. Your agent can install the
source checkout now; future release downloads and pinned Homebrew installation are
covered in [distribution](docs/distribution.md).

[Agent-first setup](#start-with-your-agent) · [Manual setup](#manual-installation-alternative) ·
[Knowledge packs](#teach-your-conventions) · [Team deployment](#team-deployment) ·
[Contribute](#contribute) · [Documentation](#documentation)

## Start with your agent

You do not need to run a terminal command, start a server, create a project or
add repositories in the UI. Give your host coding agent this request:

> Set up DiffMind for me using AGENT_SETUP.md in this repository. Install and
> register its full-management MCP connection, create or reuse my work project,
> preview and import these repositories: REPOSITORIES_OR_ORG. Build the graph,
> wait for completion, and verify known dependencies with source evidence.
> Perform the setup yourself; do not give me commands or UI chores.

[AGENT_SETUP.md](AGENT_SETUP.md) is the executable playbook for the host agent.
It runs the source installer, receives machine-readable MCP configuration, and
registers it with the user's client. The client launches `diffmind agent`, which
starts its own backend on an available loopback port. No project needs to exist.
The dashboard remains optional; the agent can return its URL when requested.

The full-management connection exposes:

- **describe_management**, **inspect_workspace**, **manage_workspace**: discover
  operations, create/configure projects, import repositories, build/refresh graphs,
  monitor/cancel/retry jobs, edit packs/configuration, and administer access,
  tokens and quotas. It reuses the API's validation, permissions and audit log.
- **agent_runtime**: inspect/start/stop/restart the owned backend and persist its
  refresh schedule and worker settings.
- **agent_command**: scaffold/lint/test/explain/install packs, run doctor, and
  perform backups or queue maintenance. It drains/restarts the backend around
  commands; it is a bounded DiffMind command interface, not an arbitrary shell.
- The existing **11 graph tools** for services, evidence, dependencies, impact,
  search, history and tracing.

You authorize repository access and any client/system installation permissions;
the agent does the operational work. An MCP server cannot grant itself initial
permission inside its host client or obtain company credentials on your behalf.
The bootstrap requires a host agent with authorized shell/configuration access.

Local agent mode has full workspace authority. It stops its backend when the
owning agent disconnects or crashes; work/history persist and reconnect starts
it again. One lifecycle owner is allowed per home. Additional agents can connect
to that backend's HTTP `/mcp`, or use an always-on
[shared deployment](docs/company-deployment.md). Remote agents have the rights
of their authenticated identity; viewer tokens remain read-only.

See [agent operations](docs/agent-operations.md) for tool inputs, lifecycle,
recovery, teaching patterns and the tested end-to-end workflow.

## Manual installation (alternative)

The following is optional for people who prefer operating DiffMind themselves.
It is not required by the agent-first workflow above.

Source builds require **Go 1.26.6 or newer**, **Git**, and a **C compiler** for
tree-sitter/CGO. Supported targets are macOS and Linux, on Intel/AMD64 and ARM64;
Windows binaries are not provided. On macOS, the Xcode Command Line Tools supply
Git and the compiler (`xcode-select --install` if missing). On Linux, install
your distribution's Git and C build tools. Use the Go version in [go.mod](go.mod),
rather than assuming the OS package is recent enough.

Node.js and Docker are **not needed for normal local use**. Both web interfaces
are embedded. Node.js 24 is needed only to rebuild them; Docker is optional for
containerized indexing or shared deployment.

```bash
git clone https://github.com/mohammad-safakhou/diffmind.git
cd diffmind
git switch master
# Already have this checkout? Install its committed implementation from here.
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/diffmind
export PATH="$HOME/.local/bin:$PATH"
diffmind version --json
```

Keep that PATH setting in your shell configuration for new terminals. A source
build may report `dev`/`unknown` version fields; that is not an installation
failure. To run without installing, use `make build` and `./bin/diffmind` instead.

Create a separate workspace for your work trial, outside any source repository:

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
mkdir -p "$DIFFMIND_HOME"
chmod 700 "$DIFFMIND_HOME"
diffmind doctor
diffmind ui --no-spa-rebuild
```

Open **http://127.0.0.1:8090**. The server stays in the foreground; Ctrl+C stops
it. Warnings about no projects/graphs on first use are expected. The
`--no-spa-rebuild` flag uses the embedded UI without looking for Node in a source
checkout. Keep the same `DIFFMIND_HOME` whenever you restart or connect an agent.

1. Create a project, then choose **Import & build**.
2. Choose **Local directory** and a folder containing your Git repositories, or
   **GitHub org** and your organization name. Start with a few related services.
   Use **Include regex** and **Dry run only** to preview the selection.
3. Disable **Dry run only** and start **Import and build graph**. Wait for
   completion, review repository errors, then inspect services, edges and their
   source evidence. A partial result can contain older analysis: check freshness.
4. Use **Update graph** after changes. Unchanged, verified analysis is reused;
   **Operations** shows history, cancellation and retries. **Compare graphs**
   compares saved versions.

Local imports use existing paths, not copies; they are not automatically pulled
from Git remotes. GitHub imports create managed clones and sync their configured
branches. For private GitHub access, supply a read-only `GITHUB_TOKEN` to the
server using approved secret handling. Do not paste it into source files or
agent settings. See [personal setup](docs/personal-setup.md) for exact credential,
daily refresh, backup and troubleshooting steps.

Want to test without company data? Follow the
[synthetic three-service quickstart](docs/contributor-quickstart.md): a Go gateway
calls Python catalog and Java billing services.

## Manual read-only agent connection

For full management use the agent-first setup above (`diffmind agent`). The
following keeps the original read-only `diffmind mcp` integration available.

Build the graph first, then find its project ID:

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
diffmind list projects
```

For Codex, replace `PROJECT_ID` with that ID:

```bash
codex mcp add diffmind-work --env DIFFMIND_HOME="$DIFFMIND_HOME" -- \
  "$HOME/.local/bin/diffmind" mcp --project PROJECT_ID
```

The client starts MCP itself; do not run `diffmind mcp` in another terminal.
The UI can be stopped when querying saved graphs, but must run for UI actions
or scheduled refresh. Absolute paths and an explicit home avoid accidentally
opening a different workspace from a GUI-launched agent.

Ask your agent:

> Use DiffMind to list this project's services. Explain what depends on
> SERVICE_NAME, cite source evidence, and report graph freshness and unresolved
> dependencies. Do not infer missing relationships as facts.

The same stdio server works with Claude Code, Cursor and other MCP clients.
[Agent setup examples](docs/personal-setup.md#connect-an-agent) include their
configuration and connection checks. MCP offers service discovery, search,
dependency traversal, change impact, graph comparison and exact-ID local tracing.
It reads persisted artifacts; it does not refresh repositories or modify code.

Local stdio is trusted workspace access. `--project` chooses a default,
**not an authorization boundary**. For restricted shared access, use HTTP MCP
with a [project-scoped viewer token](docs/agent-tokens.md).

## Privacy and current limits

- Analysis and graph storage run on your machine, without an LLM. Git imports
  contact your Git provider; optional developer/indexer tooling may use the
  network. This is not a sandbox for untrusted repositories.
- Graphs can contain internal names, URLs, paths and source evidence. MCP results
  enter your agent's context and may reach its model provider. Use only
  company-approved agents and data policies.
- `DIFFMIND_HOME` defaults to `~/.diffmind`. Keep company workspaces and backups
  private, outside Git and public file-sharing folders. Backups are not encrypted.
- Static analysis is not a runtime inventory or a guarantee of full coverage.
  Dynamic URLs, wrappers and unsupported conventions can leave gaps. Check the
  [tested support matrix](docs/supported-patterns.md) and validate known
  relationships before relying on the graph at work.
- One server writes each workspace. Distributed workers, automatic SSO group
  provisioning and automatic workspace-path relocation are not implemented.
  See the [roadmap](docs/ROADMAP.md) and
  [verification record](docs/readiness-verification.md) for scope and evidence.

## Teach your conventions

Knowledge packs add service identities, configuration mappings, HTTP/RPC
relationships and queue conventions without an LLM or executable plugin.

```bash
diffmind pack init ./my-pack --id example.conventions
# Adapt the rules and synthetic positive/negative fixtures first.
diffmind pack lint ./my-pack
diffmind pack test ./my-pack
diffmind pack explain ./my-pack --repo /absolute/path/to/a/service
diffmind pack install ./my-pack
```

Use **Update graph** in the same workspace after installation. Packs are
versioned and integrity-checked. Keep proprietary packs private; contribute only
sanitized rules and synthetic fixtures. Language semantics belong in AST
detectors, not broad source regexes. See [pack authoring](docs/knowledge-packs.md),
[service manifests](packs/service-manifest/README.md), and
[OpenFeign configuration](packs/spring-openfeign-config/README.md).

## Team deployment

Follow [company deployment](docs/company-deployment.md). From a source checkout
with Docker Engine and Compose, create `.env` only if it does not already exist:

```bash
cp .env.example .env
chmod 600 .env
# Edit .env: set DIFFMIND_AUTH_TOKEN to a secret from `openssl rand -hex 32`.
docker compose up -d --build
docker compose ps
```

Compose publishes port 8090 on the host; restrict network access before starting
it. Browser authentication accepts any
username and uses the shared token as the password. That token grants global
admin access: keep it out of developers' agent settings. Before team access,
configure TLS and authenticated ingress, restrict direct backend access, and use
[project permissions](docs/project-access.md) and
[expiring viewer tokens](docs/agent-tokens.md) for agents at `/mcp`.

Compose persists data in `diffmind-data` and enables refresh on startup and every
15 minutes. The local binary does **not** schedule refresh by default. Host
source paths are not automatically available in containers; use managed clones
or deliberate mounts. `docker compose down -v` deletes the workspace volume.

[Operations](docs/operations.md) covers signed GitHub webhooks, bounded queues,
workers, project quotas and metrics. [SQLite queue storage](docs/queue-storage.md)
is optional for larger histories. [Backup/recovery](docs/backup-recovery.md) and
[backup automation](docs/backup-automation.md) cover offline snapshots, rotation,
restoration and the opt-in systemd timer, not live/distributed backups.

## Commands and API

```text
diffmind ui                       Start the workspace (also: diffmind)
diffmind doctor [--json]           Check installation and graph readiness
diffmind version [--json]          Show build information
diffmind list projects            List project IDs
diffmind list runs --project ID    List saved project graphs
diffmind run --repo PATH           Analyze one repository
diffmind validate --run ID         Validate an analysis run
diffmind list-runs                 List analysis runs
diffmind graph --project ID        Build a graph from existing analysis
diffmind mcp [--project ID]        Start read-only stdio MCP
diffmind agent [--project ID]      Agent-owned backend and full-management MCP
diffmind pack                      Knowledge-pack command help
diffmind backup                    Offline backup/restore/rotation help
diffmind storage                   Queue migration/verification help
diffmind extractor-ui              Low-level analysis dashboard
```

The running UI serves read-only graph queries, including:

```text
GET /api/v1/projects
GET /api/v1/projects/{id}/graph/summary
GET /api/v1/projects/{id}/services
GET /api/v1/projects/{id}/dependencies?service=NAME&direction=both
GET /api/v1/projects/{id}/impact?target=NAME&depth=6
GET /api/v1/projects/{id}/search?q=QUERY&limit=50
```

Single-graph queries accept
`run=<completed-run-id>`; comparisons require explicit `from` and `to` IDs.
[Graph history and tracing](docs/graph-history.md) documents routes and parameters.
Shared-server requests require authentication; only `/healthz` is public.

## Contribute

Start with [CONTRIBUTING](CONTRIBUTING.md) and the
[synthetic contributor quickstart](docs/contributor-quickstart.md). Pull requests
target `master`. Keep company code, internal URLs, tokens and workspace artifacts
out of issues and contributions. Report vulnerabilities privately using
[SECURITY.md](SECURITY.md).

```bash
make test                 # Go tests, including real CLI company acceptance
make test-packs           # Official pack fixtures and graph assertions
make test-race            # Go race detector
make ui-build             # npm ci + build both interfaces (Node 24)
make ui-test              # Frontend helpers and component tests
make build
# Complete local verification (also needs Ruby and network access):
make verify
```

External SCIP integrations need additional language toolchains/indexers:
`make test-integration` opts into them. Native release and container validation
are separate; local tests do not certify every platform or company's patterns.

```text
cmd/diffmind/          unified CLI
internal/extractor/    deterministic repository analysis
internal/workspace/    projects, graph, query API, MCP and web UI
protocol/              shared service-document model and validation
packs/                 official tested knowledge packs
testdata/              public synthetic fixtures
docs/                  setup, operations, architecture and design references
```

## Documentation

- [Host-agent setup playbook](AGENT_SETUP.md) and [agent operations](docs/agent-operations.md)
- [Personal installation and work trial](docs/personal-setup.md)
- [Import, incremental refresh and recovery](docs/ingestion.md)
- [Graph history, evidence and tracing](docs/graph-history.md)
- [Supported patterns](docs/supported-patterns.md) and [pack authoring](docs/knowledge-packs.md)
- [Company deployment](docs/company-deployment.md), [permissions](docs/project-access.md) and [agent tokens](docs/agent-tokens.md)
- [Operations](docs/operations.md), [queue storage](docs/queue-storage.md) and [backup/recovery](docs/backup-recovery.md)
- [Architecture](docs/ARCHITECTURE.md), [roadmap](docs/ROADMAP.md) and [readiness evidence](docs/readiness-verification.md)
- [Distribution and release maintenance](docs/distribution.md)

## License

Apache License 2.0. See [LICENSE](LICENSE).
