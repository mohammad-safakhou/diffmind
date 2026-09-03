# Personal installation and work trial

This is the **optional manual workflow**. To have your agent install and manage
everything, use [the host-agent setup playbook](../AGENT_SETUP.md) and
[agent operations](agent-operations.md). No manual project creation, repository
import or server startup is required for that path.

Start with one local workspace and a few services whose relationships you know.
Validate their evidence before expanding to the whole organization. You do not
need Docker, a server deployment, or a model API key for this workflow.

## Install the current checkout

Public release assets are not yet published. Build the committed source with
Go 1.26.6 or newer, Git and a C compiler (CGO enabled). On macOS, run
`xcode-select --install` if the Command Line Tools are missing. On Linux, install
Git and your distribution's C build tools. Node is unnecessary unless you are
changing the embedded web interfaces.

```bash
git clone https://github.com/mohammad-safakhou/diffmind.git
cd diffmind
git switch master
# Already have the checkout? Run the remaining commands there.
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install ./cmd/diffmind
export PATH="$HOME/.local/bin:$PATH"
command -v diffmind
diffmind version --json
```

Add `export PATH="$HOME/.local/bin:$PATH"` once to your shell startup file
(for example `~/.zshrc` for interactive zsh); do not overwrite that file.
`command -v diffmind` identifies which installation runs. Use the absolute
binary path for agents. Source builds can show `dev`/`unknown` metadata;
keep `git rev-parse HEAD` with trial notes to identify the source revision.

Alternatively use `make build` and the absolute path to `bin/diffmind`, without
installing. The public installer, Go `@latest` and Homebrew HEAD use remote
state, not unpushed local commits. See [distribution](distribution.md).

## Start an isolated work workspace

Choose a persistent directory outside Git or cloud-shared folders. This keeps
work data separate from the default `~/.diffmind`:

```bash
export PATH="$HOME/.local/bin:$PATH"
export DIFFMIND_HOME="$HOME/.diffmind-work"
mkdir -p "$DIFFMIND_HOME"
chmod 700 "$DIFFMIND_HOME"
diffmind doctor
diffmind ui --no-spa-rebuild
```

Open **http://127.0.0.1:8090**. Keep the terminal running; Ctrl+C stops the
server. Loopback-only local mode is trusted local access, without a login. Do
not forward its port or expose it to other users. One server writes a workspace;
a second is rejected. `--no-spa-rebuild` uses embedded assets without Node.

Set the same `DIFFMIND_HOME` in each new terminal. Omitting it opens
`~/.diffmind` instead. Graphs, history, configuration and managed clones persist
after shutdown. Initial `doctor` warnings about no projects/graphs are normal.
Docker is optional; finding its executable does not verify its daemon is running.

## Import your first services

Confirm company policy permits local analysis and storing derived architecture
data. This is not a sandbox for untrusted source or developer tooling. Start
with 3–10 related services.

### Existing local checkouts

Use a directory containing repositories, for example:

```text
/absolute/path/to/work-repositories/
  gateway/.git/
  catalog/.git/
  billing/.git/
```

1. Create a project and open **Import & build → Local directory**.
2. Enter that parent directory. Narrow **Include regex**, for example
   `^(gateway|catalog|billing)$` (replace these synthetic names).
3. Select **Dry run only**, click **Preview import**, and review the candidates.
4. Turn off **Dry run only**, keep the analysis/graph pipeline enabled, and
   choose **Import and build graph**.
5. Wait for completion and inspect its errors, counts and graph evidence.

Local import registers existing paths; it does not copy them into the workspace
or pull their remotes. You control branches and updates. Avoid editing during
analysis when you need a graph tied to a clean revision. Dirty checkouts can be
analyzed but disable incremental reuse. Repository-local configuration and
optional tooling can affect analysis; use trusted checkouts and normal backups.

### GitHub organization and private repositories

Use a company-approved read-only credential for the selected repositories,
including any required organization SSO authorization. Supply `GITHUB_TOKEN`
to the **server process** through your approved secret manager. If GitHub CLI
already holds the correct account, this alternative avoids writing the literal
token into shell history:

```bash
# Stop the server first; DIFFMIND_HOME must still be set.
gh auth status
GITHUB_TOKEN="$(gh auth token)" diffmind ui --no-spa-rebuild
```

Only use that account if its scope is appropriate. For GitHub Enterprise, select
the correct hostname in GitHub CLI and the matching **API base** in the import
dialog. Do not send a credential to an arbitrary endpoint. Environment values
remain sensitive; use your organization's approved handling. DiffMind can also
discover an authenticated GitHub CLI credential when available.

Choose **Import & build → GitHub org**, enter the organization, narrow
**Include regex**, and preview with **Dry run only** before importing. Keep the
full pipeline enabled. DiffMind creates managed clones, syncs their configured
branches, analyzes and builds the graph. With a token, automatic clone transport
prefers HTTPS. SSH needs working credentials and host trust in the server
environment; private repository discovery still needs GitHub API access.

Repository default branches come from GitHub unless overridden: do not assume
they all use `master`. Keep managed clones free of edits; sync refuses to
overwrite dirty work. For other Git hosts, start with local clones rather than
assuming GitLab/Bitbucket organization discovery is supported.

## Check the graph before relying on it

Verify several known relationships: caller, target service, endpoint/queue, and
file/line evidence. Check an external dependency and a known non-relationship
too. A plausible-looking or nonempty graph is not proof of complete coverage.

Read [tested support and exclusions](supported-patterns.md). Dynamic targets,
custom wrappers and conventions may need a pack or detector change. A `partial`
ingestion can leave older successful analysis in the graph; check freshness.

In another terminal:

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
diffmind doctor
diffmind list projects
diffmind list runs --project PROJECT_ID
curl --fail http://127.0.0.1:8090/api/v1/projects
curl --fail http://127.0.0.1:8090/api/v1/projects/PROJECT_ID/graph/summary
```

Replace `PROJECT_ID` with the ID from `list projects`, not the display name.
These curl commands assume a local unauthenticated loopback server. Shared
servers require authentication.

## Connect an agent

Use the absolute installed binary path, the same `DIFFMIND_HOME`, and the project
ID. The client launches `diffmind mcp` itself. Saved graphs can be queried with
the UI stopped; MCP does not import, refresh or modify repositories.
Restart/reconnect your client after setup.

### Codex

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
codex mcp add diffmind-work --env DIFFMIND_HOME="$DIFFMIND_HOME" -- \
  "$HOME/.local/bin/diffmind" mcp --project PROJECT_ID
codex mcp list
```

### Claude Code

Run this in the source project where you use Claude Code. Local scope keeps
the configuration personal to that project rather than writing a shared file:

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
claude mcp add --scope local --env DIFFMIND_HOME="$DIFFMIND_HOME" \
  --transport stdio diffmind-work -- \
  "$HOME/.local/bin/diffmind" mcp --project PROJECT_ID
claude mcp list
```

See [Claude Code's MCP and scope documentation](https://code.claude.com/docs/en/mcp).

### Cursor or another JSON-configured client

Merge this entry into your existing configuration without replacing other
servers. For personal Cursor configuration, use `~/.cursor/mcp.json`. Replace
every placeholder with an absolute path or actual project ID; this example
does not rely on shell expansion of `~` or environment variables:

```json
{
  "mcpServers": {
    "diffmind-work": {
      "command": "/absolute/path/to/.local/bin/diffmind",
      "args": ["mcp", "--project", "PROJECT_ID"],
      "env": {
        "DIFFMIND_HOME": "/absolute/path/to/.diffmind-work"
      }
    }
  }
}
```

See [Cursor's MCP configuration guide](https://cursor.com/docs/mcp). Other clients
use the same command, arguments and environment in their own format.

### Verify and protect the connection

Ask the agent to list services using DiffMind, inspect a known service's
dependencies, and cite evidence and graph freshness. Compare the answer with
the UI. For comparisons, have it list graph runs and select explicit run IDs.

MCP results enter the agent's context and may reach its model provider; only
connect company-approved agents. Reading graphs does not need GitHub tokens,
so do not put them in MCP settings. Local stdio trusts your OS account;
`--project` is a default, not isolation from other projects in that workspace.
Use [scoped HTTP project tokens](agent-tokens.md) for restricted shared access.

## Daily refresh

Restart with the same home and `diffmind ui --no-spa-rebuild`. Pull **local**
repositories yourself, then choose **Update graph**. Managed Git repositories
are synced by DiffMind. Unchanged, verified successful analysis is reused;
changed inputs are reanalyzed. **Compare graphs** shows saved versions and
**Operations** records attempts and errors.

Optional scheduling on a laptop (only while the server runs):

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
diffmind ui --no-spa-rebuild --refresh-on-start --refresh-interval 15m
```

Supply Git credentials again if needed. Without these flags or matching
environment settings, local startup does not schedule refresh. Closing the
terminal or sleeping the laptop does not provide an always-on service.
[Company deployment](company-deployment.md) covers that use case.

**Cancel ingestion** stops work; **Resume / retry** recovers direct ingestions.
Retry queued refreshes in **Operations**. Do not delete locks or state to bypass
errors. See [ingestion recovery](ingestion.md).

## Teach missing patterns

Record missing/incorrect relationships privately with minimal examples. For
configuration conventions, create a pack outside the DiffMind checkout, adapt
its rules and synthetic positive/negative fixtures, test, and install it:

```bash
diffmind pack init ./my-pack --id example.conventions
diffmind pack lint ./my-pack
diffmind pack test ./my-pack
diffmind pack explain ./my-pack --repo /absolute/path/to/service
diffmind pack install ./my-pack
```

Use the same home, then **Update graph** and recheck expected facts and negative
cases. [Pack authoring](knowledge-packs.md) explains supported declarations and
when an AST detector is needed. Do not submit company code/artifacts as tests.

## Back up and upgrade

Stop the UI, stop/disconnect stdio MCP processes and stop other commands using
the workspace. Prevent agent auto-restarts during maintenance. Use a private
directory outside the workspace and a new archive name:

```bash
export DIFFMIND_HOME="$HOME/.diffmind-work"
mkdir -p "$HOME/.diffmind-backups"
chmod 700 "$HOME/.diffmind-backups"
diffmind backup create --offline \
  --output "$HOME/.diffmind-backups/before-upgrade-001.tar.gz" --json
diffmind backup verify \
  --archive "$HOME/.diffmind-backups/before-upgrade-001.tar.gz" --json
```

Backups include the workspace, not external local repositories or environment
credentials: protect those separately. Archives are **not encrypted** and can
contain company code/secrets. Save the reported digest separately.
[Backup/recovery](backup-recovery.md) explains restoration and path limitations;
[managed rotation](backup-automation.md) is optional.

Update a clean DiffMind checkout with `git pull --ff-only` only after the desired
commits are on the remote. Preserve local work first; do not reset/rebase to
force an upgrade. Re-run installation, check `diffmind doctor`, restart UI/agent
and test known queries. Keep the previous binary and backup for rollback.
Changing `DIFFMIND_HOME` does not relocate an existing workspace automatically.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Command missing or older binary runs | Set PATH; check `command -v diffmind`; use the absolute installed path. |
| Compiler/toolchain build failure | Check `go version`, `go env CGO_ENABLED CC`, and the C compiler; this build uses CGO. |
| Download installer returns 404 | No binary release yet; use the committed source checkout. |
| Startup asks for npm | Use `--no-spa-rebuild` for embedded assets. |
| Port 8090 busy | Use `diffmind ui --no-spa-rebuild --port 8091`; do not kill an unrelated server. |
| UI has projects but agent sees none | Compare their absolute home and binary paths, then restart the agent. |
| Empty or incomplete graph | Finish ingestion, inspect repository errors, check patterns/freshness; doctor does not validate accuracy. |
| Private GitHub import fails | Check account, read permissions, SSO, API base and server credentials. |
| Local checkout is not pulled | Local imports are not managed clones; update them yourself. |
| Managed clone is dirty | Preserve changes before restoring a clean checkout; sync will not overwrite them. |
| Workspace busy / backup rejected | Stop the relevant server/agent/writers and retry; do not unlink locks. |

Public bug reports should include the source revision/version, OS/arch, sanitized
error and a synthetic reproducer with expected facts. Do not attach company
graphs, source, backups or credential-bearing logs.
