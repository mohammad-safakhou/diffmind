# Project-scoped agent tokens

An administrator can give a developer's agent access to **one project** without
giving it the shared administrator token or requiring an identity proxy to
authenticate that agent. Tokens work with the HTTP API and remote Streamable
HTTP MCP at `/mcp`. No OAuth flow or agent-specific integration is required by
Diffmind: the client must support a bearer header, preferably sourced from its
secret store or an environment variable.

## Use it

1. Run the server with `DIFFMIND_PROJECT_ACCESS=scoped` (or
   `diffmind ui --project-access scoped`). Keep the shared recovery token or
   trusted-proxy administrator identity private. Protect a shared deployment
   with HTTPS and authenticated ingress; do not expose the unauthenticated local
   mode to other users.
2. As an administrator, open a project, then **Project access → Agent tokens**.
   Choose a descriptive owner/purpose name, **Viewer**, and an expiry. The UI
   defaults to 30 days. Use **Editor** only for an integration that must queue,
   retry or cancel refresh work over HTTP; MCP tools are always read-only.
3. Save the displayed secret in the client's protected configuration, then hide
   it. The secret is returned once; reloading the page cannot recover it.
4. Configure the client with your server's HTTPS `/mcp` URL and
   `Authorization: Bearer <project token>`. It can discover only that project;
   an omitted project selector resolves to that sole accessible project.
5. Test discovery and a graph query before distributing access. The graph must
   already have been built by an administrator or refresh worker.

For a client-independent HTTP check, supply the secret through your environment
without placing its literal value in shell history:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer $DIFFMIND_AGENT_TOKEN" \
  https://diffmind.example.com/api/v1/projects
```

Headers can be visible to local process inspection with some command-line
clients. Use your deployment's approved secret handling for production. Never
put a token in a URL, repository, screenshot, log, or shared agent settings file.
The UI retains a newly issued secret only in page memory until hidden/unmounted;
it does not write it to local storage or copy it automatically.

If an identity proxy sits in front, configure an appropriate non-interactive
route that passes the bearer credential to Diffmind without replacing it with a
browser login redirect. Keep the backend inaccessible directly and strip all
client-supplied Diffmind identity headers. Do not send the proxy secret to an
agent. A recognized project token takes precedence over proxy/admin identity
headers; malformed, revoked or expired tokens never fall back to a stronger
identity on the same request.

## Scope and lifecycle

Tokens are **service grants independent of user memberships**, not personal
tokens impersonating the administrator who issued them. Removing a user from
`access.json` does **not** revoke their separately issued tokens. Record an
owner/purpose in the name and revoke their tokens during offboarding. A token
cannot grant admin, manage memberships/tokens, access other projects, configure
host paths, import repositories, change packs, or access fleet metrics.

A project grant exposes the entire project, including repository metadata,
configuration, evidence and saved history; it is not per-repository redaction.
Use separate projects for separate data-access boundaries. The editor role has
the same restricted refresh controls as a scoped proxy editor. See the
[permission matrix](project-access.md#effective-permissions).

Rotation is explicit: issue a replacement, update/test the client, then revoke
the old token. Revocation is permanent and idempotent. The first revocation
timestamp and actor are retained. New requests check current storage and expiry;
live run streams recheck before events and every second while idle. Revocation
does not retract downloaded data or an already-computing response, and does
not cancel already-admitted refresh jobs. Cancel those separately if required.

Switching the server to legacy mode disables project-token authentication and
issuance, rather than broadening token access. Administrators can still list and
revoke old tokens. Re-enabling scoped mode re-enables unexpired, unrevoked
tokens. Existing shared-server tokens and local stdio MCP remain fully trusted.
The `dmt1.` prefix is reserved for project credentials, not shared admin tokens.

## Administration API

These routes require a global administrator, including in legacy mode:

```text
GET  /api/v1/projects/PROJECT_ID/tokens
POST /api/v1/projects/PROJECT_ID/tokens
POST /api/v1/projects/PROJECT_ID/tokens/TOKEN_ID/revoke
```

Issue body (all three fields are required):

```json
{"name":"Developer laptop agent","role":"viewer","expires_in_seconds":2592000}
```

Names are exact, nonempty, at most 100 UTF-8 bytes and have no control characters
or surrounding whitespace. Roles are `viewer` or `editor`. Lifetime is an integer
from 60 to 31,536,000 seconds (365 days). Oversized bodies, unknown fields,
trailing data and invalid input return 400. Issuance in legacy mode or at the
1,000-record project history limit returns 409. No records are automatically
pruned; the count includes revoked/expired tokens to retain the audit history.

Issuance returns 201 with `{ "token": { ...metadata }, "secret": "dmt1.…" }`.
GET returns `{ "enabled": true, "tokens": [ ...newest-first metadata ] }`.
Metadata contains `id`, `project_id`, `name`, `role`, `created_at`, `expires_at`,
`created_by`, and optional `revoked_at`/`revoked_by`. Revocation returns that
metadata with 200; a missing ID returns 404. Administration responses are
`Cache-Control: no-store`. Never automatically retry an issuance after an
uncertain response: reload the list and revoke the unreceived credential first.

`GET /api/v1/session` identifies token callers with `auth_method: project_token`,
`token_id`, and `token_project`. Mutation audits record a stable
`project-token:<project>:<id>` actor, not the credential or verifier. Read/MCP
auditing remains the ingress's responsibility; redact authorization headers.

## Persistence, recovery, and limits

Each credential has a random 128-bit ID and a 256-bit secret. Only a SHA-256
verifier of the whole credential is persisted, never the plaintext secret.
Each project's versioned `tokens.json` registry uses private 0600 atomic file
replacement, file and directory sync, bounded reads and validation. Corrupt,
future-format or symlink registries fail closed with generic 503 errors; they
are never silently overwritten. Invalid/expired/revoked credentials return
401. Administrators retain ordinary recovery access if a registry is corrupt.

The registry uses the current single-server writer model, not a distributed
credential database. Protect the data directory: an OS user who can modify it
can change authorization. Token grants do not sandbox analyzers/repositories.
There is no group provisioning, delegated issuance, automatic renewal, secret
recovery, token history pruning, or built-in request rate limiter. Apply request
limits at your HTTPS ingress.

[Offline backups](backup-recovery.md) preserve token verifiers, grant metadata
and original timestamps. **Restoring an older snapshot also restores its access
state:** a token revoked after that snapshot can become valid again. Keep
external access disabled during recovery, review/revoke or rotate restored
tokens before reopening, and protect backups as sensitive data. A token revoked
before the snapshot remains revoked after restore. An expired token remains
expired according to its original absolute expiry; restore does not renew it.

## Verification

The test suite covers issuance validation, hash-only persistence, permissions,
concurrent issuance/revocation, expiry boundaries, restart/rotation, corruption,
symlinks, cross-project route isolation, all MCP tools, identity switching,
revocation of open streams, and secret-free mutation auditing. The real-analyzer
Go/Python/Java acceptance workflow queries its graph using a scoped credential
and verifies token history with the actual backup/restore CLI for JSON and
SQLite queue deployments. DOM component tests exercise issue/hide/confirm/cancel,
revocation, legacy-mode gating and API failures through the real fetch wrapper;
they do not substitute for native browser layout/focus/TLS checks.
Run `go test ./...`, `go test -race ./...`, and the
workspace UI's `npm test` and `npm run build`.
