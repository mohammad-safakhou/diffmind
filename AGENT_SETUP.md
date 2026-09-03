# DiffMind: host-agent setup playbook

Audience: a coding agent with the user's permission to install software, edit
its own client configuration, and access the repositories the user names.
**Do the work. Do not turn these instructions into a checklist for the user.**
The UI and command-line workflow are optional alternatives, not onboarding steps
that the user must perform.

## Outcome

From a clean machine/workspace, install the current DiffMind implementation,
register **full-management** MCP, create or reuse a project, discover/import the
authorized repositories, build a graph, wait for completion, and verify its
evidence through MCP. Report the resulting project and useful findings.

Only ask the user for genuinely missing choices or authority: repository/org
scope, company credentials/SSO approval, system installation permission, or a
client integration approval that the host requires. Never bypass those boundaries.
An MCP server cannot install or authorize itself before its host can launch it.

## 1. Inspect and install (agent executes)

1. Resolve this source checkout; inspect its Git status/revision. Preserve all
   existing work and history. Do not reset, rewrite, push, or publish as a setup
   side effect. An existing local checkout may contain newer code than the remote.
2. Choose a dedicated private workspace outside Git, normally
   `$HOME/.diffmind-work`, and a user-owned binary directory, normally
   `$HOME/.local/bin`. Inspect existing paths/configuration before changing them.
3. Check Git, Go >=1.26.6 and a C/CGO compiler. Node and Docker are not required.
   If prerequisites are missing, install them using authorized platform tools;
   request approval if necessary instead of asking the user to perform the
   installation. Do not silently use sudo or alter system security policy.
4. Run the following **yourself** from the checkout:

```sh
go run ./scripts/agent-setup --repo-root . \
  --home "$HOME/.diffmind-work" --bin-dir "$HOME/.local/bin" \
  --name diffmind-work
```

The installer builds the current checkout into a private staging directory,
runs doctor in isolated state, installs the executable and emits JSON containing
the absolute binary/home and `mcpServers` entry. It never starts a persistent
system service, reads company repositories, or overwrites client settings.
If the binary already exists, inspect it, arrange backup/stop any owned runtime
for an upgrade, then use `--replace` only for that authorized target. Existing
workspaces must be private real directories; do not chmod an unrelated directory
just to satisfy setup. A failed build leaves an existing binary intact.

There is no published binary release yet. Do not hand the user a release URL
that does not exist. After releases are available, the host agent may use the
verified installer instead, then register the same `agent` launch command.

## 2. Register the full-management connection (agent executes)

Use the JSON returned by setup: an absolute `command`, `args: ["agent"]`, and
explicit `DIFFMIND_HOME`. **Do not substitute `mcp`**: that is the legacy
read-only mode and cannot onboard or manage the workspace.

Use the host client's native configuration tool when available. Otherwise make
a targeted edit/CLI registration, preserving all unrelated MCP servers. Inspect
a same-named entry before replacing it. Keep configuration personal unless the
user explicitly wants to share it. Do not store GitHub tokens in MCP settings.

Examples for the host agent, not the end user:

```sh
# Codex: verify command support using the installed CLI's help.
codex mcp add diffmind-work --env DIFFMIND_HOME="$HOME/.diffmind-work" -- \
  "$HOME/.local/bin/diffmind" agent

# Claude Code: personal configuration scoped to the current source project.
claude mcp add --scope local --env DIFFMIND_HOME="$HOME/.diffmind-work" \
  --transport stdio diffmind-work -- "$HOME/.local/bin/diffmind" agent
```

For Cursor, merge the returned `mcpServers` entry into the user's
`~/.cursor/mcp.json`; do not overwrite the file. For another MCP client, use
its command/arguments/environment registration format. Paths in JSON must be
absolute, not unexpanded shell expressions.

Reload/reconnect the MCP integration using the host's supported mechanism.
If the client requires a user approval or restart, explain only that required
action; never claim tools are live before discovery succeeds. Do not ask the
user to launch a server. Their client launches DiffMind, and DiffMind launches
its own backend.

Verify discovery of 16 tools: 11 graph tools plus `describe_management`,
`inspect_workspace`, `manage_workspace`, `agent_runtime` and `agent_command`.
Use `agent_runtime(action="status")` to confirm the intended home/backend.
If another process owns the workspace, inspect it; do not kill it or unlink its
locks. An agent can connect to that service's HTTP MCP with the appropriate
authority, or arrange an authorized handoff. Only one local lifecycle controller
owns a home; additional query/management clients can share its HTTP endpoint.

## 3. Onboard repositories entirely through MCP

1. Call `describe_management`, then `list_projects`. Reuse the intended
   project if it exists; otherwise `manage_workspace` with operation
   `create_project` and body `{"name":"Work architecture"}`. Record its returned
   ID. After an uncertain creation response, list before retrying.
2. Resolve only the directories/org/repositories the user authorized. For a local
   collection, use `import_repositories` with `provider:"local"`, absolute
   `root`, and `dry_run:true`. For GitHub use `provider:"github"`, `org`,
   include/exclude regex and an explicit limit where useful. Credentials must be
   available in the backend environment or approved GitHub CLI account. Obtain
   them through approved secret handling, never by asking for a token in chat.
3. Inspect the preview and narrow it to scope. Start `start_ingestion` with an
   `import` body using the same fields, without dry-run. For existing
   repositories, an empty body runs incremental sync/analysis/graph construction.
4. Poll `inspect_workspace(operation="get_ingestion")` using the returned
   project selector until terminal. A 202 is acceptance, **not completion**.
   Report partial/failed runs honestly, inspect errors, repair authorized
   configuration, and retry the appropriate ingestion/job. Do not wipe state.
5. Query `list_services`, `get_dependencies` and `get_service` for several
   known relationships. Inspect evidence and freshness. Missing static-analysis
   facts are unknown, not proof of no dependency. Record patterns requiring
   teaching rather than inventing edges.
6. If requested, configure refresh via `agent_runtime`: read current settings,
   preserve unrelated values, then `configure` with a complete settings object.
   The local backend runs only while its owning agent connection is alive.
7. Offer useful findings and the optional dashboard URL from runtime status.
   Do not direct the user to create projects or import repositories in the UI.

## 4. Teach, recover and maintain

- Read [agent operations](docs/agent-operations.md) and
  [supported patterns](docs/supported-patterns.md).
- Use `agent_command` for pack init/lint/test/explain/install, doctor,
  backup/verify/restore/rotation and queue storage migration/verification.
  It invokes only this DiffMind binary, not a shell. The host agent can author
  fixtures/rules using its normal approved file tools, then test/install them.
- Use `manage_workspace` for project packs, repository configuration, quotas,
  job cancellation/retry and access/token administration.
- Inspect before destructive actions. The `confirm` argument repeats the exact
  destructive operation; it is an intentionality guard, not permission to act
  outside the user's request. Never delete source or history to fix a test.
- Commands stop/restart the owned backend for maintenance. Other external
  processes may still hold leases; do not bypass them. Preserve backups and
  existing source revisions. Backups are private but not encrypted.
- MCP data reaches the host agent and potentially its model provider. Use only
  company-approved clients. Keep credentials and proprietary code out of public
  contributions, reports and setup logs.
- For an always-on team instance, the authorized deployment agent follows
  [company deployment](docs/company-deployment.md), configures TLS/identity and
  connects MCP at `/mcp`. Admins can manage the full API; scoped editors retain
  restricted refresh controls, and viewers remain read-only. Local host lifecycle
  and command tools are not remotely exposed.

## Acceptance before telling the user setup is finished

Confirm the configured binary/home, live full-management tools, intended project
and repository selection, terminal successful ingestion, nonempty expected graph,
and source-backed queries. Explain actual unsupported patterns and freshness.
If the user did not supply repositories, complete installation/registration first
and ask only for their repository/org scope. Never claim onboarding succeeded
on an empty project or tool discovery alone.
