# Project access

Shared Diffmind installations can restrict each trusted-proxy user to explicit
projects across the UI, HTTP API, and remote MCP. This is **opt-in**:
`DIFFMIND_PROJECT_ACCESS=legacy` is the default and retains global roles.
`scoped` denies non-admin access unless a project membership or an explicitly
issued [project agent token](agent-tokens.md) grants it. This also
applies to old projects and newly created projects; no grants are inferred.

## Enable safely

1. Configure the [trusted identity proxy](company-deployment.md#authentication-roles-and-tls).
   It must strip client-supplied `X-DiffMind-*` headers, authenticate users, and
   inject the secret, stable user subject, and global role. Block direct access
   to Diffmind from untrusted networks and use TLS. Keep a global admin recovery
   identity or server token available.
2. As an admin, open each project's **Project access** screen. Add the exact,
   case-sensitive `X-DiffMind-User` values and choose viewer or editor. Grants
   can be prepared while still in legacy mode; the screen warns that they are
   not enforced yet. Names with whitespace around them are rejected, not trimmed.
3. Set `DIFFMIND_PROJECT_ACCESS=scoped` in the server environment or Compose
   `.env`, then restart/recreate the service. The binary also accepts
   `diffmind ui --project-access scoped`. Invalid modes prevent startup.
4. Test with a non-admin identity: only its projects should appear, guessed
   project URLs should fail, and its agent should discover the same projects.
   Grant changes require no restart. Changing the mode does require a restart.

Global admins can always access every project, including membership management.
The shared server token (also accepted as the browser Basic password), unauthenticated local mode, and
`--allow-unauthenticated` identity are **global admin**, not scoped credentials.
Do not distribute the recovery token to ordinary users or their agents. Local
stdio MCP trusts the OS user and is not restricted by these HTTP memberships.

## Effective permissions

In scoped mode the proxy's global role is a ceiling on a project grant. A global
viewer with an editor grant is still read-only. A global editor with a viewer
grant is also read-only. No membership means no project access, except for
global admins. Project memberships cannot grant admin.

| Operation in scoped mode | Project viewer | Project editor | Global admin |
| --- | --- | --- | --- |
| Read project, repositories, packs, graphs, evidence, history, queries and MCP | Yes | Yes | All projects |
| Queue refresh; cancel/retry queued work; cancel ingestion/graph run | No | Yes | Yes |
| Create/delete projects; import/discover repositories; change paths, packs, config or analyzer options | No | No | Yes |
| Direct analysis, ingestion resume, or manual graph assembly | No | No | Yes |
| Read/change memberships or issue/revoke agent tokens | No | No | Yes |
| Fleet refresh/status, global analysis discovery, metrics | No | No | Yes |

Editors use **Update graph** or **Operations** to queue work using saved project
configuration. Filesystem-sensitive setup stays admin-only because this service
shares one host and analyzer credentials. A project grant exposes the entire
project, including saved source evidence, paths and configuration; it is not a
per-repository or field-level privacy filter. Only import data suitable for all
members of that project. Existing global editor behavior is retained in legacy
mode, except membership administration always requires a global admin.

Project and job listings are filtered before pagination and totals. Missing and
inaccessible project IDs return the same 404. A sole accessible project can be
the default MCP project even when other projects exist on the server. Malformed
or unreadable policies fail closed with a generic 503 instead of granting access;
list requests fail rather than silently omit policy errors.

## Membership API and persistence

All requests still require normal server authentication. Admin endpoints:

```text
GET /api/v1/projects/PROJECT_ID/access
PUT /api/v1/projects/PROJECT_ID/access
```

GET returns `version`, `revision`, `members`, and `updated_at`. A project without
a saved policy starts at revision 0 with no members. PUT replaces the complete
member map and must include the revision last read:

```json
{"revision":0,"members":{"user-123":"viewer","user-456":"editor"}}
```

A successful update increments the revision. A stale revision returns 409;
reload and review the changes rather than blindly retrying. An empty member
object revokes all proxy-user memberships, not independently issued agent tokens.
Subjects are limited to 256 UTF-8 bytes,
cannot contain control characters, and at most 1,000 members are supported per
project. Invalid input returns 400. Policies are atomic JSON records at
`projects/<id>/access.json`, not credentials. Their grants, revision, and original
timestamp are preserved by [offline backup/restore](backup-recovery.md). Recover
a corrupt policy from a trusted backup with the server stopped; do not switch
to legacy mode as a workaround, since that would remove all project restrictions.

`GET /api/v1/session` includes `project_access` alongside global identity/role.
`GET /api/v1/projects/PROJECT_ID/capabilities` returns the effective role and
`can_refresh`, `can_configure`, `can_delete`, `can_manage_access` for that project.
The UI uses these capabilities; server authorization remains authoritative.
Membership mutations, failures, and conflicts use the existing mutation audit
log. Read/MCP-query auditing belongs at the identity proxy.

## Agents and revocation

For agents without proxy-issued credentials, an administrator can issue a
[project-scoped agent token](agent-tokens.md) on the Project access screen.
It works directly as a bearer credential, expires, and can be revoked. These are
separate service grants: user offboarding must also revoke their issued tokens.

Alternatively, route each remote MCP request through the same authenticated proxy so the
request carries its user's stable identity. The proxy's agent authentication
must support non-interactive clients (for example an IdP-issued credential
validated by the proxy). Diffmind does not issue per-user tokens or implement
OAuth login. Browser login cookies alone do not configure an agent, and the
shared admin bearer-token example in the deployment guide does not enforce
per-user grants. Never give the proxy secret to clients.

Scoped `/mcp` is stateless Streamable HTTP: a fresh scoped query service is
created for each request, so a previous MCP session cannot retain another
identity's access. Legacy mode keeps its existing stateful transport. All MCP
tools remain read-only. HTTP/MCP requests recheck current memberships, and live
run event streams check before each event and at one-second intervals when idle.
The workspace polls capabilities and hides its data after it observes revocation.

Revocation stops new access; it cannot retract downloaded information or a
response already being computed. Already-admitted jobs continue independently
of the requesting user's membership; cancel them separately as an authorized
editor/admin if necessary. Scheduled and signed webhook refreshes use their own
server authorization, not user grants.

This is application-level project authorization, not a sandbox for hostile
repositories or OS users. Admins and people who can read the data directory can
read all projects. Distributed persistence, group synchronization, delegated
project admins and per-project execution quotas remain
future work.

## Verification

The automated suite covers policy validation, atomic concurrent updates,
restart/backup preservation, every registered project route's nonmember denial,
filtered list totals, role ceilings, downgrades, admin recovery, corrupt-policy
failure, traversal selectors, all MCP tools, identity switching, revocation, and
closing an idle live event stream. Run `go test ./...` and `go test -race ./...`;
workspace frontend tests cover member validation and route/action helpers.
