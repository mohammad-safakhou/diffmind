# Agent-managed operations

DiffMind supports an agent-operated product workflow, not just agent queries.
The [host-agent playbook](../AGENT_SETUP.md) handles installation and MCP
registration. Users provide intent and access; the agent operates the platform.

## Connection modes and authority

| Connection | Tools | Authority/lifecycle |
| --- | --- | --- |
| Local `diffmind agent` | 11 graph + 3 management + 2 host tools | Full local workspace control; starts backend automatically, owns it until disconnect/crash |
| Local `diffmind mcp` | 11 graph tools | Original trusted read-only integration; no backend ownership |
| HTTP `/mcp`, viewer | 11 graph tools | Read-only, restricted to accessible projects |
| HTTP `/mcp`, editor/admin | Graph and management tools | Same role/membership/host-operation checks as the HTTP API; no local lifecycle/CLI tools |

Local mode is trusted OS-level access, not a sandbox. Its backend binds only
loopback on an automatically reserved port, without shared-server authentication;
do not expose/forward it. Use a secured shared deployment on multi-user or remote
hosts. `--project` is an optional default selector, not a permission boundary.
Admin tokens grant full platform administration; viewer tokens are intentionally
insufficient when the user wants the agent to manage projects.

## Discover, inspect, mutate

`describe_management` lists the finite operation catalog, method/path,
description, body example and destructive marker. Supply `operation` to inspect
one entry. It documents projects, repositories, imports, ingestion, jobs, packs,
configuration, access, credentials, quotas, graph runs and pull request inspection.
Every existing browser mutation except signed provider webhook delivery is
covered by a catalog operation; an automated route-parity test guards this.

`inspect_workspace` accepts GET operations. `manage_workspace` accepts
mutations. Both take:

```json
{
  "operation": "start_ingestion",
  "selectors": {"pid": "PROJECT_ID"},
  "body": {
    "import": {
      "provider": "local",
      "root": "/absolute/path/to/repositories",
      "include": "^(gateway|catalog|billing)$"
    },
    "concurrency": 2
  }
}
```

Selectors correspond to placeholders in the catalog: `pid`, `rid` (repository
or graph run, depending on the operation), `jid`, `pack_id`, `tid`, etc.
Query parameters use a `query` string map. There is no arbitrary URL/header/method
input. Traversal, extra/missing selectors, wrong read/write tool and oversized
bodies are rejected before routing. Maximum body 1 MiB, response 8 MiB; use
focused queries/pagination if necessary.

Responses include `status`, `data`, and `retry_after` when applicable.
HTTP failures become MCP tool errors retaining status and structured error data.
No mutation is automatically retried. Destructive operations require
`confirm` equal to their exact operation name, for example `delete_project`.
Read the current object and preserve unrelated configuration before replacement.
This is not a substitute for the user's authorization.

## End-to-end workflow

1. `list_projects`; `create_project` only if needed.
2. `import_repositories` with `dry_run:true` to preview authorized repositories.
3. `start_ingestion` to import/sync/analyze/build, or `body:{}` for incremental
   refresh of registered repositories.
4. Poll `get_ingestion` until terminal; inspect errors and freshness for partial
   results. `get_live_status`, `list_repositories`, `ingestion_history` and
   `list_jobs` explain progress.
5. Use graph tools for source-backed queries. `list_graph_runs` and
   `compare_graphs` inspect saved versions.
6. Queue later work with `enqueue_refresh`; cancel/retry using `cancel_job` and
   `retry_job`. Direct ingestion uses `cancel_ingestion`/`resume_ingestion`.

GitHub credentials come from the server environment or approved GitHub CLI
account, not tool body arguments. Local registrations retain their paths and
are not automatically pulled; the host agent can update authorized local
checkouts using its normal Git tools. Managed clones are synced by DiffMind
and refuse dirty checkout overwrites.

## Local runtime and maintenance tools

`agent_runtime` accepts `status`, `start`, `stop`, `restart`, or `configure`.
Status includes the home, optional dashboard URL and effective settings.
Configure requires the complete settings object; read before editing:

```json
{
  "action": "configure",
  "settings": {
    "refresh_interval": "15m",
    "refresh_on_start": false,
    "refresh_concurrency": 4,
    "project_access": "legacy",
    "repository_workers": 4,
    "job_workers": 2,
    "queue_capacity": 256
  }
}
```

Use `project_access:"scoped"` before administering restricted project tokens;
the local owner retains full OS-trusted authority. Settings persist privately in
`agent-settings.json`; unknown/corrupt/public or
symlink configurations fail closed. Configuration restarts the backend; old
settings are restored if startup/publication fails. `0` or empty interval
disables scheduled refresh. The dashboard port may change after restart; read
status again. Stop does not erase data; subsequent management starts the backend.

One local controller holds a separate lifecycle lock even during maintenance.
Do not run two controllers for one home. Additional clients may use that
backend's HTTP MCP while it lives. Normal disconnect stops the child; an inherited
lifetime pipe also stops it if the controller is killed. Work/history remain
durable; reconnect starts recovery. This is not an always-on OS service: use
shared deployment for refresh independent of developer agent sessions.

`agent_command` invokes only the current installed binary with an argument
array. Allowed command families are `doctor`, `version`, `pack`, `backup`
and `storage`; no shell, arbitrary executable or environment overrides exist.
Commands are serialized with management, limited to five minutes and 1 MiB of
output, and report exit status, truncation and backend-restart outcome.

```json
{"args":["pack","init","/absolute/new/path/my-pack","--id","example.conventions"]}
```

The host agent edits synthetic fixtures and rules, then calls pack lint, test,
explain and install. Project-scoped pack CRUD is also available through
management tools: use the storage key returned by create/list as `pack_id`, and
preserve the manifest's `id` when updating (these can differ for dotted IDs).
See [pack authoring](knowledge-packs.md) for schema and limits.

```json
{"args":["backup","create","--offline","--output","/private/backups/new.tar.gz","--json"]}
```

The owned backend is stopped before maintenance and restored afterward, including
command failure/cancellation. Other external writers/old read-only MCP clients
may be using the same home. Stopping cancels active work and persists recovery
state; inspect running jobs before intentionally interrupting them. Those writers
may still block an exclusive lease: do not bypass their locks.
`backup restore`, `backup rotate` and `storage migrate` require `confirm`
equal to that two-word command. Existing offline flags, non-overwrite/path
guards and retention semantics still apply. For restore, use an absent target;
automatic workspace relocation is not implemented.
[Backup/recovery](backup-recovery.md) explains preservation and confidentiality.

## Security and tests

Remote mutation callbacks use the **current HTTP tool request's credentials**,
not cached initialization credentials. They re-enter the same authentication,
role/project checks, mutation guards, queue quotas and audit middleware as the
UI. Identity changes or token revocation cannot inherit a previous admin's
management rights. Tool annotations describe effects; they do not authorize them.

Viewer tokens remain query-only. Scoped editors can refresh assigned projects,
but cannot import, configure host paths, change packs, issue tokens or administer
access. Administrators can perform these operations. Local host tools are not
available on remote MCP; a deployment agent uses authorized host tooling for
installation, service lifecycle and offline maintenance.

`TestAgentAcceptance` launches the actual installed binary over stdio and performs
empty-workspace onboarding, three-repository graph extraction/query, incremental
reuse, pack testing/installation, failed-command recovery, backup, SQLite
migration, persistent scheduling, reconnect and controller-crash cleanup through
MCP. Permission, identity-switch, mutation-route parity, request bounds and setup
tests complement the existing company acceptance/race suites. Native release
gates run the same agent acceptance test against their installed archive.
