# Ingestion, incremental updates, and recovery

Use **Import & build** for a local directory or GitHub organization. Use
**Update graph** to refresh repositories already registered in the project.
The UI displays repository progress, newly analyzed counts, and reused counts.

## What is reused

Only a previously successful analysis with intact, matching artifacts can be
reused. Its fingerprint includes Git HEAD, useful source/configuration bytes
(including Git-ignored inputs), central and explicit runtime configuration,
local service hints, project/repository configuration, verified installed packs,
project pack content, analyzer identity, and run options. Checkpoints are saved
after each successful repository, not only at the end of the project.

Dirty/non-Git checkouts, symlinks, submodules, unknown analyzer identity, or
unreadable inputs disable reuse. Changed inputs during analysis prevent a reuse
checkpoint. This is a conservative optimization, not an immutable source
snapshot: avoid editing repositories during a scan when you need a graph
attributable to one clean revision.

Remote repositories are synced before the cache check. The graph is rebuilt each
time, even when artifacts are reused, so resolution uses current configuration.
Scheduled refresh uses the same checks. Per-project scan concurrency defaults to
four and is capped at sixteen. A global budget (default four) also bounds active
sync/analyzer operations across projects, including direct repository actions.

## Cancel and resume

**Cancel ingestion** stops queued work, cancels active analysis, and cancels a
graph build if one is running. On macOS/Linux analyzers run in their own process
group, allowing cancellation to stop their children too. The project remains
locked until cancellation drains. Cancelled jobs stay stopped across restarts.

**Resume / retry** is available for failed, partial, interrupted, and cancelled
direct ingestions with a saved request. Refresh-queue ingestions must be retried
from **Operations**, keeping retries within their owning job's attempt history.
Resuming keeps the ingestion ID and increments its attempt.
Completed imports need not be repeated. Successful repositories are revalidated
and reused; failed, changed, or uncacheable repositories are analyzed again.
This is checkpoint recovery, not resuming halfway through one source file.

Jobs interrupted by shutdown or a crash resume when the server starts. Older
jobs without a saved request remain visible but require a new **Import & build**.
Credentials come from runtime configuration; requests do not store GitHub tokens.

When some repositories fail, a graph may use available artifacts, including
previous analysis for a failed repository. The ingestion is marked `partial`,
not successful: review errors and freshness before relying on it. Failed or
cancelled runs do not remove older completed graphs.

## HTTP controls

These routes use existing editor/admin authorization and mutation audit logging.

| Request | Meaning |
| --- | --- |
| `GET /api/projects/{pid}/ingestion` | Latest persisted job and repository progress |
| `POST /api/projects/{pid}/ingestion` with `{}` | Incremental update |
| Same POST with `{"force":true}` | Analyze every service repository again |
| `POST /api/projects/{pid}/ingestion/cancel` | Request cancellation; returns 202 while work drains |
| `POST /api/projects/{pid}/ingestion/resume` | Resume/retry the latest recoverable job |

Poll GET until status is no longer `running`. Overlapping starts/project mutations
return 409. Resume rechecks the saved job under the project lock.
`projects/<id>/ingestion.json` retains only the latest job and current attempt.
Each attempt is also checkpointed under
`projects/<id>/ingestions/<ingestion-id>/attempt-<number>.json`. The latest
projection is kept for compatibility. Earlier versions' overwritten attempts
cannot be reconstructed; the surviving latest record is preserved when work
next advances. Request bodies are omitted from the history API.
Back up the entire data volume, including analysis and versioned graph artifacts.

## Verification and limits

Run `go test ./internal/workspace/ui -run TestCompanyAcceptance -v`. This builds
the real CLI and checks three synthetic Git repositories through ingestion,
graph generation, source evidence, HTTP/MCP queries, invalidation, and retry.
`make verify` runs the broader suites, race detector, UI tests/builds, pack
fixtures, dependency audits, and vulnerability checks.

The [operations queue](operations.md) adds signed webhooks, durable refresh
attempts, bounded retries, and a global repository-operation budget. Direct
ingestion remains immediately started and rejects same-project overlap; queued
refreshes wait for busy projects. Distributed execution, project-scoped roles,
and automatic backup/migration tooling remain future work.
