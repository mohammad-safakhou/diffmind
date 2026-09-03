# Indexed queue storage

Diffmind offers an opt-in SQLite backend for refresh jobs and their attempt
indexes. It removes full historical JSON reads from admission, worker polling,
job pagination and metrics. No separate database service or SQLite CLI is needed.
Existing and new installations continue using JSON until explicitly migrated.

This is queue persistence, **not distributed workers**. Projects, memberships,
repositories, ingestions, checkouts and graph artifacts remain filesystem-backed.
Use one server per home directory on local storage with reliable locks. Network
filesystems, multiple replicas, remote workers and per-project quotas remain
unsupported.

## Migrate

Use the new binary throughout. Stop the server, stdio MCP, analysis/graph commands
and all other writers. Make an offline backup before migration:

```bash
export DIFFMIND_HOME=/srv/diffmind
diffmind backup create --offline --output /backups/before-queue-migration.tar.gz --json
diffmind storage verify --offline --json
diffmind storage migrate --offline --json
diffmind storage verify --offline --json
```

Then start one server normally. There is no backend environment toggle: the
published `queue/` directory activates SQLite. For Compose, stop the service and
run these as one-shot commands using the same image, user and data volume, plus
an external writable backup mount for backup creation. Maintenance commands
take an exclusive lease; `--offline` acknowledges that older binaries and direct
filesystem writers must also be stopped.

Migration imports `jobs/*.json` into private staging, validates records, builds
indexes, commits, verifies SQLite integrity and index/payload agreement, and
closes the database. Atomic **no-replace** directory publication activates it;
directory syncs follow. Original job files are never deleted or rewritten. The
initial database retains their exact JSON bytes, IDs, delivery digests, attempt
numbers, statuses and timestamps. Subsequent mutations use normal lifecycle
rules: current timestamps advance, historical timestamps remain unchanged.

Reports include backend, schema version, job/attempt counts and a digest of the
original imported file set. The digest is migration provenance, not a checksum
of future live database contents. Repeating migration verifies the database and
reports `already_migrated`; it never reimports stale JSON.

Malformed records, unsupported fields, symlinked sources, conflicting active
project claims, or job files above 32 MiB abort before activation. A killed
process may leave an unpublished `.queue-migrate-*` directory; inspect it offline
before removing it. If publication succeeds but directory sync fails, the error
explicitly reports activation: verify storage before starting the server.

## Runtime guarantees

The database is `queue/queue.sqlite`, application-identified with schema version
1. Missing, corrupt, or unsupported active databases fail closed; an existing
`queue/` never falls back to historical JSON. Normal requests validate the
database identity/schema, not its entire contents.

The backend uses rollback journaling, `synchronous=FULL`, foreign keys and
immediate transactions with a five-second lock wait. Capacity, deduplication and
admission share one transaction. Each claim persists its attempt before work
starts; a unique partial index allows at most one running job per project.
Cancellation, retry, backoff, webhook deduplication and recovery share the same
lifecycle implementation as JSON. These follow SQLite's
[atomic commit model](https://sqlite.org/atomiccommit.html) and depend on reliable
filesystem synchronization/locking. The pinned
[Go driver](https://pkg.go.dev/modernc.org/sqlite@v1.58.0) is pure Go.

Analysis remains **at least once**: graph completion and queue completion are
not one transaction. Interrupted attempts remain in history and follow the
existing retry budget. SQLite alone does not provide distributed execution
fencing or transactional filesystem graph publication.

Authorized project IDs filter job queries before counts and pagination. An
empty authorization list returns no jobs, not global access. Ordering matches
JSON, including subsecond timestamps and ID tie-breaking. Concurrent inserts
can still shift offset-based pages. Scoped queries support up to 10,000 project
IDs; metrics and large offsets still have index scan cost. No history is pruned.

CLI servers take `.diffmind-server.lock` before opening/recovering state. A second
local server is rejected while analyzer children and read-only MCP clients remain
allowed. Kernel locks release on exit; never unlink lock files to bypass them.
Embedded/library users must arrange their own single-server ownership. This is
not a multi-host lease.

## Verify and recover

`storage verify --offline` checks page integrity, every job payload against its
indexed fields, attempt counts/statuses/durations, and orphaned attempts. This is
an intentional full scan, not automatic repair. Unexpected storage failures stop
queue admission/workers rather than acknowledge unsaved work. Preserve failed
state for diagnosis and restore a trusted [offline backup](backup-recovery.md).

Whole-home backups include the database and any journal, original JSON,
memberships and graph artifacts. Stop writers: copying a live database file
alone is not a supported backup. Restore the complete snapshot, verify storage,
then start one server. Real-binary acceptance tests cover both backends.

**Never downgrade by deleting `queue/` or running an older binary.** Original
JSON stops receiving updates after migration. Reusing it could replay completed
work or forget attempts/cancellations. Rollback requires restoring the complete
pre-migration backup to a new/absent destination, with writers stopped. Preserve
the newer workspace too: the older snapshot cannot contain post-migration work.
Automatic export/downgrade and general metadata-schema migration are not provided.

## Verification and performance

Tests cover both backend contracts, subprocess contention, killed uncommitted
transactions, corruption/future schemas, failed/repeated migrations, original
bytes/timestamps, query index plans, permission filtering, metrics, single-server
locks and real CLI refresh/backup/restore.

```bash
go test ./...
go test -race ./...
go test ./internal/workspace/store -run '^$' \
  -bench BenchmarkQueueClaimWithHistory -benchtime=3x -benchmem
```

The benchmark measures idle claims against 10,000 historical jobs, excluding
fixture setup/migration. A local macOS/arm64 run measured approximately 7.57 s
per JSON poll versus 0.65 ms for SQLite (three timed iterations each). This is a
local storage microbenchmark, not a production SLA, analyzer-throughput result
or proof of distributed scalability. Rerun on your target storage.
