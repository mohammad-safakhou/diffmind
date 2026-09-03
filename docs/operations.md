# Continuous operations

Diffmind's single-process refresh queue connects manual, scheduled, and GitHub
push-triggered updates to the same incremental ingestion pipeline. It does not
require an LLM, external queue, or database service. Direct **Import & build** and
**Update graph** remain available; the queue waits if those operations already
own the project.

With [scoped project access](project-access.md), **Update graph** uses the queue;
initial import/configuration is admin-only. Viewers/editors see only assigned
projects and their jobs. The proxy role and project grant both limit access.

For large retained job histories, opt into [indexed SQLite queue storage](queue-storage.md).
Migration preserves job/attempt timestamps; the same APIs and operations screen
work with either backend. The default remains JSON and one server per workspace.

## Start and observe

The binary starts workers automatically. Configuration is read once at startup:

```bash
export DIFFMIND_JOB_WORKERS=2
export DIFFMIND_QUEUE_CAPACITY=256
export DIFFMIND_REPOSITORY_WORKERS=4
diffmind ui --refresh-interval 15m
```

Equivalent flags are `--job-workers`, `--queue-capacity`, and
`--repository-workers`. Compose and `.env.example` include the same settings.
Project workers accept 1–16; capacity accepts 1–10000; the global repository
budget accepts 1–32. Defaults are 2, 256, and 4. Per-project refresh concurrency
defaults to 4 and is capped at 16. Repository processing uses a fixed-size worker
pool, not one goroutine per repository.

Open **Operations** in a project. Viewers see refresh jobs and ingestion attempts;
editors/admins can queue, cancel, and retry. The screen polls every three seconds,
has separate history pagination, and links saved graph artifacts. The new
operations are intentionally not agent MCP tools: MCP remains read-only.

## Per-project resource limits

In **Operations → Project resource limits**, a global administrator can set
`max_pending_jobs` (0–10000) and `repository_workers` (0–32). **0 inherits the
server ceiling**, not unlimited capacity or a disabled project. Effective limits
are the smaller of the configured project cap and server ceiling. Existing
projects inherit by default. Readers can inspect their project's configured and
effective limits and a usage snapshot; reload the panel to refresh that snapshot.
Only global admins can save limits, in both legacy and scoped access modes.

Pending jobs means queued **plus running**, including delayed automatic retries.
Manual, fleet/scheduled, signed webhook and explicit retry admissions enforce
the cap atomically inside the queue store, on either JSON or SQLite. Project
overflow returns **429** with `Retry-After: 10`; global overflow still returns
503. If both caps are full, the global error takes precedence. Accepted duplicate
requests still return their original job; rejected webhook deliveries leave no
receipt and may be redelivered after capacity becomes available. Automatic
attempts retain their already-admitted slot; explicitly retrying a terminal job
requires a new slot and preserves earlier attempts and timestamps.

The repository cap covers actual cloning/fetching and analyzer execution across
direct actions, imports and queued refreshes. Admission acquires project and
global capacity together: a project waiting at its cap does not consume global
slots. Raising a cap wakes waiting work; lowering it lets active operations drain
before admitting more. Lowering the pending cap below current usage likewise
blocks new work without deleting or cancelling existing jobs.

Settings are versioned in `projects/<id>/limits.json`, atomically persisted with
optimistic revisions, and included in offline backups. Corrupt policies reject
new admissions, never silently fall back to defaults. Already accepted duplicate
requests and job history remain readable. Do not edit policy files while the
server is running: use the API/UI so waiting repository work is notified.

```text
GET /api/v1/projects/PROJECT_ID/limits
PUT /api/v1/projects/PROJECT_ID/limits
{"revision":0,"max_pending_jobs":8,"repository_workers":2}
```

GET returns `limits`, `effective_pending_jobs`, `effective_repository_workers`,
`pending_jobs`, and `active_repository_workers`. PUT requires all three fields
shown above and returns the saved policy with its new revision and UTC update
time. Read the current revision first; stale saves return 409. Invalid requests
return 400 and unavailable policies/storage return 503. Reload after an uncertain
save instead of blindly retrying the old revision. These settings are not MCP
mutation tools.

These are single-server concurrency/admission caps, not CPU/memory/disk quotas,
OS isolation, fair-share scheduling, or reserved throughput. Direct graph builds
are outside the repository budget; direct imports are outside the pending-job
queue cap, but their sync/analyzer work uses the repository budget. Standalone
CLI extraction in a separate process does not share the server's limiter.

## Queue semantics

- A successful enqueue is persisted before returning 202. JSON jobs are stored
  under `DIFFMIND_HOME/jobs/`; migrated jobs live in `queue/queue.sqlite`. Both
  preserve every attempt and its timestamps.
- Workers claim eligible jobs oldest-first, skipping projects with a running
  queued job. Existing ingestion/repository/graph work defers the job by one
  second without spending an attempt. Concurrent jobs for different projects
  respect the worker limit.
- Sync and analyzer execution also acquire a single shared repository budget,
  including direct repository actions. Graph builds are not repository slots;
  queued graph builds remain within the project worker budget. Direct imports
  and graph builds are not admitted through the refresh queue.
- Failed/partial attempts retry automatically, normally after 2 and 4 seconds,
  up to three attempts. Explicit retry keeps earlier attempts and allows three
  more, capped at 100 attempts per job. Beyond that, enqueue a new refresh.
  Queue-owned ingestions cannot bypass this lifecycle through the direct
  ingestion resume API; retry their job from **Operations** instead.
- A queued cancellation is terminal immediately. A running cancellation first
  persists intent, then signals the ingestion and drains its processes before
  releasing the worker. Cancelled jobs do not restart automatically.
- Shutdown interrupts running attempts; interrupted attempts are retained and
  requeued within their remaining attempt allowance. On crash recovery, claimed
  jobs are marked interrupted before workers resume. Completed checkpoints are
  revalidated before reuse. Execution is **at least once**, not exactly once:
  a crash between graph completion and job completion can create another graph.
- Repeated manual/fleet triggers coalesce only when an equivalent request is
  already queued. A trigger during running work can enqueue a later update.
  The legacy fleet-refresh endpoint still rejects overlapping fleet requests.
- Capacity includes queued and running jobs globally. At capacity, enqueue
  returns 503 with `Retry-After: 10`; it does not acknowledge work it cannot save.
  A fleet request larger than available capacity reports which projects were
  not admitted; retry later or wait for the next scheduled fleet run.

The latest `projects/<id>/ingestion.json` is a compatibility projection. Durable
ingestion attempts live in `projects/<id>/ingestions/<id>/attempt-*.json`, covering
direct imports as well as queued refreshes. Previous attempt numbers, errors,
start/end times, repository outcomes, and graph references remain available.
Existing old workspaces preserve their surviving latest record when advancing;
history already overwritten by older releases cannot be recovered.

Store writes use a temporary file in the target directory, sync its bytes, then
atomically rename it. This prevents concurrent readers seeing half-written JSON.
Job corruption/persistence errors stop the scheduler and reject new admission;
they are not silently treated as an empty queue. This is process-crash recovery,
not a transactional guarantee across all files or a substitute for volume
backups/power-loss-safe storage.

## GitHub push webhooks

1. Register the project's repositories first, with their real Git URLs and
   tracked branch (`master` is supported; there is no assumption of `main`).
2. Generate a separate secret, for example `openssl rand -hex 32`, and supply it
   as `DIFFMIND_WEBHOOK_SECRET`. Blank disables webhooks. Never reuse an admin
   token or commit the secret. Restart Diffmind after changing it.
3. In the repository or organization webhook settings, choose JSON, push events,
   the same secret, and this HTTPS endpoint:
   `https://diffmind.example.com/api/v1/projects/PROJECT_ID/webhooks/github`.
4. Route this exact POST endpoint through your reverse proxy without its
   interactive login requirement. Keep all other paths protected and enforce
   TLS, a request-rate limit, and body-size limits at the proxy.

The handler verifies the raw-body `X-Hub-Signature-256` HMAC before parsing or
reading project data, using a constant-time comparison. This follows
[GitHub's signature guidance](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).
It supports `ping` and `push`, with a 2 MiB payload ceiling. Unsupported events,
tags, deleted branches, unregistered repositories, host mismatches, and pushes
to untracked branches are ignored without queuing work. Repository matching
normalizes HTTPS/SSH URLs; it uses the configured branch, or the signed
repository default branch when no branch was configured.

Only already registered repositories can trigger a **project-wide** incremental
refresh. Payload clone URLs/commit hashes are never passed to Git; sync uses the
saved repository configuration and current tracked branch head. This is not
per-commit replay or push-specific graph attribution. A first clone honors its
configured branch; a missing branch fails rather than reporting a stale checkout
as successfully updated. Dirty managed checkouts remain protected.

Accepted delivery IDs are hashed with the project ID into durable job identities.
The same ID/body returns the existing job, including after a restart; reusing an
ID with different body bytes returns 409. Cancelling a job does not erase this
receipt. Different delivery IDs are different jobs, even for identical payloads.
HMAC does not add a signed timestamp: do not describe this as general replay
prevention. Backpressure plus proxy rate limiting are important for bursts.
Webhook bodies and secrets are not saved in job history. The webhook response
contains only job ID/status/deduplication state, not graph data. Invalid
signatures return 401; disabled webhooks return 404; oversize bodies return 413.

No external webhook, token, or proxy configuration is installed automatically.
Organization webhooks should target each project that tracks that organization;
the endpoint does not discover/import repositories or fan out across projects.

## HTTP API

Normal server authentication applies to all routes below. Legacy mode uses
global roles; scoped mode requires project membership. Read access is
viewer/editor/admin; queue/cancel/retry requires effective editor/admin access.
Metrics and fleet operations are global-admin-only in scoped mode.

```text
GET  /api/v1/jobs?project=PROJECT_ID&offset=0&limit=50
POST /api/v1/projects/PROJECT_ID/refresh-jobs
POST /api/v1/jobs/JOB_ID/cancel
POST /api/v1/jobs/JOB_ID/retry
GET  /api/v1/projects/PROJECT_ID/ingestion-history?offset=0&limit=50
GET  /metrics
```

Omit `project` to list all accessible refresh jobs. Filtering precedes totals
and pagination in scoped mode. Lists return `total` and nullable
`next_offset`; follow it to the end. Offsets are nonnegative and page size is
1–500 (default 50). The UI uses 25. Concurrent inserts may shift offset-based
pages. Job attempts are returned with their job; ingestion-history request
bodies are omitted. Unknown records return 404; incompatible cancel/retry states
return 409. The existing fleet `POST /api/v1/refresh` remains admin-only.

## Metrics

`GET /metrics` uses Prometheus text exposition and the same authentication as
read-only queries in legacy mode; scoped mode requires a global admin. Configure your scraper with the server's authentication
scheme; the endpoint is not public. Labels contain only fixed status values,
never repository paths, project names, delivery IDs, or errors.

- `diffmind_refresh_jobs{status=...}`: retained job counts, including queue depth.
- `diffmind_refresh_attempts{status=...}`: retained attempt counts.
- `diffmind_refresh_attempt_duration_seconds`: summed completed attempt duration.
- `diffmind_repository_operations_active` / `_limit`: live sync/analyzer usage.
- `diffmind_refresh_workers`: configured project worker count.
- `diffmind_scheduler_healthy`: 0 after a scheduler persistence failure, else 1.

These are gauges over retained records, not monotonic lifetime counters. Job
statistics survive restart; live repository usage does not. Alert on scheduler
health, sustained queue growth, repeated failures, and prolonged running work.

## Limits and verification

One server process must own the data directory; current CLI servers enforce a
local server lock. JSON scans filesystem job records; SQLite uses indexes for
polling/pagination and record aggregates for metrics. History and delivery receipts are
retained without automatic pruning. Distributed leases, metadata database indexing,
and automated retention
remain future work. Use [offline backup/restore](backup-recovery.md) for a stopped
workspace; do not share it between writers. See [distribution](distribution.md)
for package-manager recipes and release limits.

`go test ./...` and `go test -race ./...` cover persistence, retry/backoff,
restart/cancel intent, queue capacity, project exclusion, repository limits,
signature vectors, branch/repository filtering, delivery deduplication, roles,
metrics, and the real CLI queued incremental acceptance fixture. Frontend tests
cover action eligibility and routing; the operations UI is also smoke-tested in
a browser. See [the roadmap](ROADMAP.md) for remaining batches.

Project-limit coverage includes both queue backends, independent SQLite writers,
concurrent admission, dynamic increase/decrease, cancelled waiters, direct sync/
analysis, fleet refresh, webhook redelivery, role/token isolation, revision
conflicts, corruption, dashboard component tests, and real-analyzer backup
recovery with a one-operation project cap.
