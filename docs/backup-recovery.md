# Offline backup and recovery

Diffmind can create, verify, and restore versioned snapshots on macOS and Linux.
This is disaster recovery for workspace files (including a migrated SQLite queue), not live snapshots, schema
migration. For verified keep-last rotation and operator-installed scheduled
maintenance, see [managed backup automation](backup-automation.md).

## Create and verify

Stop the server, stdio MCP processes, graph/analysis commands, and all other
writers first. Older binaries do not honor maintenance locks.

```bash
export DIFFMIND_HOME=/srv/diffmind
diffmind backup create --offline --output /backups/diffmind-snapshot.tar.gz --json
diffmind backup verify --archive /backups/diffmind-snapshot.tar.gz --json
```

The report includes the producing version, original home, creation time, counts,
and SHA-256 of the **entire compressed archive**. Save the digest separately in
your backup system; provide it as `--sha256 DIGEST` when verifying/restoring.
Per-file checksums detect damage, but the archive's own manifest is not a
signature. Protect the archive and trusted checksum from tampering.

The source must contain `projects/`. The output parent must exist. Existing
archives are never overwritten, and output must be outside `DIFFMIND_HOME`.
Creation reads each file twice and checks both hashes; it fails if copied
content changes. Publication happens only after the complete archive is synced.

## Scope and confidentiality

**Every file under the home** is included except the maintenance lock: project
configuration, installed packs, analysis artifacts, graphs, job/delivery receipts,
ingestion history, audit logs, and managed worktrees (including `.git`) when
inside the home. Original JSON timestamps and record bytes are not rewritten.

Project `limits.json` policies, revisions and update times are included. A
restore reinstates the snapshot's caps, not later administrative changes; review
them before admitting work. Effective caps still obey the restarted server's
global configuration. Active in-memory operation counts restart at zero.

Project `tokens.json` registries contain hashed credential verifiers, grants and
creation/expiry/revocation history and are also included. **An older restore can
reactivate tokens revoked after its snapshot.** Keep external access disabled
during recovery and review/revoke or rotate restored tokens before reopening.
Absolute expiry times are preserved, not renewed. See [agent tokens](agent-tokens.md).

Archives are mode `0600` but **not encrypted**. They can contain company source
and accidentally stored secrets. Never attach them to public issues/releases.
Use your backup system for encryption, access control, off-host copies, and
retention. Environment credentials, external source directories, proxy config,
and external volumes are not copied; back up these dependencies separately.

Regular-file/directory modification times and ordinary permission bits are
preserved. Ownership, ACLs, xattrs, hard-link identity, sparse layout, and symlink
modification times are not. Restored content belongs to the restoring user.
The new workspace root is private (`0700`); its original mode/time are not copied.
Relative symlinks must point to a saved regular file/directory. Absolute,
external, dangling, and chained links are rejected. Sockets, FIFOs, devices and
unsupported archive types are rejected, not silently omitted. Use a filesystem
backup for excluded source layouts; do not delete source just to make this pass.

## Recovery drill

Verification extracts nothing. Also practice restoration to an isolated new path:

```bash
diffmind backup restore --offline \
  --archive /backups/diffmind-snapshot.tar.gz \
  --destination /recovery/diffmind-drill \
  --sha256 TRUSTED_ARCHIVE_DIGEST --allow-path-mismatch
```

The parent must exist; the destination must **not exist**, even as an empty
directory or symlink. Restore validates in a private sibling staging directory,
then uses an atomic **no-replace** rename. A competing process creating the
destination makes restore fail safely. Failed restores clean up staging files;
a killed process may leave an unpublished `.diffmind-restore-*` directory for an
operator to inspect and remove.

By default the canonical destination must match the recorded home.
`--allow-path-mismatch` permits inspection/drills, **not relocation**: absolute
repository, pack, analysis and evidence paths remain unchanged. Do not start
workers in a relocated copy until dependencies have been deliberately reviewed.

For real recovery, stop the service, verify the backup, and preserve existing
data as a rollback copy. Move it aside only after checking paths and free space.
Restore into the original now-absent path with the recorded application version.
Re-supply credentials/proxy configuration, run `diffmind doctor`, inspect saved
graphs, then start one server. Queued/interrupted jobs follow normal restart
rules; completed jobs are not reset. Check Operations before reconnecting hooks.

For [SQLite queue storage](queue-storage.md), restore the entire workspace,
including `queue/` and any database journal, then run
`diffmind storage verify --offline`. Historical `jobs/*.json` files are not an
alternative live queue after migration. Never remove `queue/` to downgrade.

For Compose, stop the service and run a one-shot container with the same
image/user, the data volume, and a separate writable backup mount for
`backup create`/`verify`. The maintenance lock is inside `/data`, shared by
containers. Restore cannot replace a mountpoint: use a new directory on a
recovery volume with `--allow-path-mismatch`, inspect it, then have the operator
promote its contents to a fresh volume mounted at the original `/data` path.
Keep the old volume for rollback. Volume promotion is not automated by this CLI;
all writers must stay stopped throughout it.

## Compatibility and limits

- Format **1** is accepted; unknown formats fail. Producing application version
  is recorded but application schemas are not transformed.
- Default expanded-file limit: **100 GiB**, configurable with `--max-bytes N`
  for create, verify and restore. Maximum 200,000 entries and 32 MiB manifest.
  File contents stream; restore needs space for the uncompressed workspace.
- Requires local filesystem locks, hard links, and atomic no-replace rename.
  Network/shared filesystems are not supported for coordination. This is not a
  full power-loss-safe multi-file transaction.
- Current CLI processes hold shared maintenance leases; backup holds an
  exclusive lease. This prevents maintenance overlap; current CLI servers also
  hold a separate local `.diffmind-server.lock` to reject second server writers.
  Never unlink either lock to bypass it. The file
  normally remains after shutdown; the kernel releases leases on process exit.
- Library/embedded users must arrange their own quiescence. `--offline` is
  required because old binaries and direct filesystem writes are not detectable.

Tests restore historical graphs and original attempts from the real three-service
CLI fixture, and cover corruption, path/link attacks, future formats, limits,
timestamps, permissions, cross-process contention, and non-overwrite publication.
