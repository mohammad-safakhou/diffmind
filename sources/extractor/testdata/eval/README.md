# Eval fixtures

Each subdirectory is one labeled fixture for the accuracy harness
(`internal/eval`, `diffmind eval`). Layout:

```
<fixture>/
  expected.json     # ground truth (see schema below)
  repo/             # the source tree to extract (repo_path may also point
                    # at a shared fixture via a relative path)
```

## expected.json

Label only the **architectural-identity** fields — the matcher keys on the same
semantic identity the pipeline dedups with, so you do NOT label line numbers or
every detail field, only:

- `http_route` / `webhook` / `outbound_http`: `details.method` + `details.path`
- `db_operation` / `cache_operation`: `details.table` (or `entity`/`cache`) +
  `details.operation` (read/write); set `platform` only for a *known* datastore
  (postgres, dynamodb, …) — leave it off for a generic/unknown store
- `queue_consumer` / `queue_publish` / `stream_consume`: `platform` +
  `details.queue`/`topic`
- `scheduled_job`: `name`
- `rpc_endpoint` / `outbound_rpc`: `details.service` + `details.method`
- `cli_command` / `command_exec`: `details.command`

`connections` are labeled by the **identity of their endpoints** (`from`/`to`),
not by hash IDs.

### The `deterministic` flag

Set `deterministic: true` when the deterministic floor (tree-sitter, no LLM) is
*expected* to recover the item. Cheap mode (`eval --mode cheap`, the CI
guardrail) scores ONLY deterministic-true items, so an LLM-only fact (non-JVM db
op, queue_publish, cache_operation, …) must be `false` — otherwise it would be
charged as a floor miss. Full mode scores everything.

## Adding a fixture

1. Drop a small repo under `<fixture>/repo/` (or point `repo_path` at an
   existing fixture).
2. Run `diffmind eval --mode cheap --fixtures testdata/eval` (or `--mode
   score-run` against a real `~/.diffmind/runs/<id>`), inspect the output, and
   copy the genuinely-correct facts into `expected.json`, marking `deterministic`
   honestly. Add the items the run *should* have found but missed.
3. Ratchet the per-objective F1 threshold up as the floor improves.
