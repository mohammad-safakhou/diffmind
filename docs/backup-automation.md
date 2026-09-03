# Managed backups and scheduled maintenance

Diffmind supports verified **offline** backup rotation on macOS and Linux and
an operator-installed systemd timer for Linux servers. Neither enabling a timer
nor stopping a real service is done by the installer. Backups require a maintenance
window; this is not live database replication or a zero-downtime snapshot API.

## Create a managed catalog

Stop all writers first, including stdio MCP and external analyzer processes.
Choose a new private directory outside the workspace and on reliable local
storage. It must not contain the workspace either. The directory must already
exist with mode `0700`; do not point this command at a shared backup bucket root.

```bash
export DIFFMIND_HOME=/srv/diffmind
install -d -m 0700 /var/backups/diffmind
diffmind backup rotate --offline --directory /var/backups/diffmind --keep-last 7 --json
diffmind backup list --directory /var/backups/diffmind --json
```

`--keep-last` is required, accepts 1–1000, and **permanently deletes older managed
snapshot archives** only after a new one is created, independently verified and
published. It never prunes live workspace projects, jobs, graph history, Git
commits or working repositories. Existing standalone archives are not adopted or removed.
`--max-bytes` defaults to 100 GiB of expanded data **per archive**, not a total
catalog/disk budget. Provision space for the retained snapshots **plus one new
snapshot** and temporary files. Compression is not a disk-space guarantee.

The first rotation initializes an empty directory with `diffmind-backups.json`,
containing a version, random catalog identity and canonical source-home path.
A nonempty uninitialized directory, a different source home, symlink catalog,
or group/world-accessible catalog is rejected. A separate nonblocking catalog
lock serializes rotators across processes; source maintenance locking still
refuses live cooperating writers. Keep both directories inaccessible to
untrusted or non-cooperating writers. This is not distributed locking.

Each `snapshot-<random-id>/` contains a private `archive.tar.gz` and `receipt.json`.
The receipt records its catalog identity, source, creation time, size, archive
format/build version and SHA-256. A rotation fully verifies **all existing managed
archives**, then creates and verifies the new one before pruning. This favors
recovery integrity over speed; large retention sets extend the maintenance
window. A corrupt or mismatched archive/receipt stops rotation without pruning.
The new snapshot is always retained, including after a wall-clock rollback.

`backup list` verifies every managed archive and returns its path, receipt data
and checksum. It does not lock source writers. It can be used when the original
home is absent, provided its parent still exists and `DIFFMIND_HOME` names its
original location. To recover a listed archive, use the existing
[verify/restore procedure](backup-recovery.md), including the trusted checksum.

Files and archive-contained timestamps remain unchanged. Retention ordering
uses snapshot creation times, not historical commit/record dates. Recovered
access grants, tokens and quotas reflect the snapshot's state; review them before
reopening external access. Receipts are not signatures: an attacker controlling
the archive **and** receipt can replace both. Keep an independent trusted copy
of checksums and protect the catalog like company source code.

## Failure and crash behavior

- A failed create or verification never prunes old snapshots.
- Publication uses a synced staging directory and atomic no-replace rename.
- A post-publication retention error exits nonzero and reports the new archive;
  it may leave extra backups. Never interpret a partial rotation as success.
- Old snapshots are moved to `.retired-<id>` before exact-file cleanup. A crash
  may leave that directory partially removed. A crash during creation may leave
  `.pending-*`. These and unrelated files are **never recursively cleaned up**
  by the next rotation. Inspect them offline before manually removing exact
  known paths; a `.pending-*` archive is not automatically trusted.
- A failed initial marker publication may leave `.catalog-*`. Inspect the
  directory before retrying; nonempty uninitialized catalogs are not adopted.
- Keep at least one independent off-host copy. These archives are unencrypted;
  this feature does not configure S3, encryption, immutable retention or backups
  of external source/volume paths.

## Linux systemd scheduling

The supplied timer and helper operate on an **existing systemd service**, not
Docker Compose. The server unit must use a current Diffmind binary, have bounded
stop time, terminate its child processes, and use the same `DIFFMIND_HOME` as the
backup environment. Configure and test that server unit first. The helper runs
as root to stop/start it and to read its workspace; resulting archives are
root-private. Do not grant users write access to the helper, environment, lock
directory or units. An editable binary path here would be root code execution.

Install reviewed files from this checkout (requires host administrator access):

```bash
sudo install -d -m 0755 /usr/local/libexec/diffmind /etc/diffmind
sudo install -m 0755 scripts/backup-systemd.sh /usr/local/libexec/diffmind/backup-systemd.sh
sudo install -m 0600 deploy/systemd/backup.env.example /etc/diffmind/backup.env
sudo install -m 0644 deploy/systemd/diffmind-backup.service deploy/systemd/diffmind-backup.timer /etc/systemd/system/
sudo install -d -m 0700 /var/backups/diffmind
```

Edit `/etc/diffmind/backup.env`: exact service name, absolute binary/home/catalog
paths, keep-last count, and private lock path. No credentials belong in these
examples. Do not use the backup service itself as the target. Units/helpers are
templates, not an instruction to overwrite existing host configuration blindly.

```bash
sudo systemd-analyze verify /etc/systemd/system/diffmind-backup.service /etc/systemd/system/diffmind-backup.timer
sudo systemctl daemon-reload
# Explicit maintenance-window drill; this stops the configured service briefly.
sudo systemctl start diffmind-backup.service
sudo journalctl -u diffmind-backup.service --since today
# Only after verifying a snapshot and a restore drill:
sudo systemctl enable --now diffmind-backup.timer
systemctl list-timers diffmind-backup.timer
```

The default is daily at 03:00 UTC with up to five minutes of jitter.
`Persistent=true` catches missed calendar runs after the timer starts, so enabling
it or booting the host can trigger maintenance outside the usual window. Change
the timer before enabling it if that is unsuitable. systemd does not start a
second instance of an already-active oneshot timer service; the helper also
locks the complete stop/backup/restart sequence. See the upstream
[timer reference](https://www.freedesktop.org/software/systemd/man/latest/systemd.timer.html).

The helper records whether the target was active. An inactive target stays
inactive; a failed/transitioning/missing target is rejected. An active target is
stopped before backup, then started on success or failure. Signal handling drains
the backup child before restarting. Private restart intent and `ExecStopPost`
also cover an abruptly killed helper; unsuccessful restarts retain the intent
for operator recovery. On reboot the runtime intent may disappear, so the main
server must have its own intended boot policy. Do not run concurrent manual
start/stop/reconfiguration during the maintenance window.

The service has a two-hour run timeout and five-minute stop timeout; tune these
for measured backup size, not guesswork. Monitor `systemctl --failed`, backup
service exit status, catalog age and disk space. A restart error is actionable
even when the backup itself succeeded. Pause scheduling with
`sudo systemctl disable --now diffmind-backup.timer` (an already-running oneshot
may still be finishing). No scheduler is installed or enabled by Diffmind itself.

## Other deployment styles

macOS can call `backup rotate` from an operator-configured launchd maintenance
window; no launchd service-management adapter is supplied. For Compose, use an
external scheduler to stop the exact app service, run the current binary in a
one-shot container with the same data volume and a separate private backup mount,
and restore the prior running state in a guaranteed cleanup step. Do not run a
backup sidecar against a live source or use `docker compose down -v`.

## Verification

`go test ./internal/workspace/backup ./cmd/diffmind ./scripts/backup-maintenance`
covers rotation, private files, catalog identity, corruption, locks/concurrency,
retention failure, CLI output, service state, stop/backup/restart failures,
interruption and post-stop recovery. Service-manager tests use isolated fakes;
Linux CI also validates the units with `systemd-analyze`. The real-company tests
perform two rotations then restore exact graph/job/token/quota history on JSON
and SQLite. Test the actual timer and restore procedure on your deployment host
before relying on it for recovery.
