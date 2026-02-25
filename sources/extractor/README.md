# DiffMind

DiffMind is an OpenCode-first architecture extractor.

It produces JSON artifacts for:
- Exposures: endpoints, queue consumers, commands, schedulers, jobs, and other external entrypoints.
- Dependencies: database usage, queue publishes, outbound endpoint calls, command runs, and other external dependencies.
- Conditional connections: exposure -> dependency links with path conditions.

## What Is Implemented

The extractor is a deterministic multi-agent orchestrator with a fixed objective map:
1. A static objective registry defines exact extraction objectives and prompts (exposure + dependency classes).
2. Objective extractor agents run all objectives in parallel, even if some return no items.
3. Detail extractor agents run per discovered item in parallel for deep, source-backed enrichment.
4. Connection extractor agents run per exposure and map conditional exposure -> dependency paths with ordered steps.
5. Results are confidence-gated, deduplicated, and emitted as artifacts plus unresolved/warning outputs.

There is no planner/verifier loop.

## OpenCode Setup (Required)

DiffMind requires a running OpenCode server.

### 1) Run OpenCode server

From an installed OpenCode CLI:

```bash
OPENCODE_SERVER_PASSWORD='your-pass' \
OPENCODE_SERVER_USERNAME='opencode' \
opencode serve --hostname 127.0.0.1 --port 4096
```

Notes:
- `serve` command is implemented in OpenCode at `packages/opencode/src/cli/cmd/serve.ts`.
- If `OPENCODE_SERVER_PASSWORD` is not set, server runs unsecured.
- If username is not set, OpenCode defaults to `opencode`.

If you do not want auth right now, run without `OPENCODE_SERVER_PASSWORD` and omit auth flags in DiffMind.

### 2) Configure provider credentials in OpenCode

Use OpenCode auth commands (interactive) before running DiffMind, for example:

```bash
opencode auth login
opencode auth list
```

## DiffMind Setup

Requirements:
- Go 1.24+
- Running OpenCode server

Install deps and run tests:

```bash
go test ./...
```

Run extraction:

```bash
go run ./cmd/diffmind run \
  --repo /absolute/path/to/target-repo \
  --opencode-url http://127.0.0.1:4096 \
  --opencode-username opencode \
  --opencode-password 'your-pass' \
  --provider-id <provider-id> \
  --model-id <model-id> \
  --workers 16 \
  --max-entities-per-objective 25 \
  --max-catalog-items 200 \
  --opencode-timeout-seconds 300 \
  --cleanup-opencode-sessions=false \
  --min-confidence 0.70 \
  --trace \
  --log-file /tmp/diffmind-trace.log
```

If OpenCode server is unsecured, remove `--opencode-username` and `--opencode-password`.

Environment-variable auth is also supported:

```bash
export OPENCODE_SERVER_USERNAME=opencode
export OPENCODE_SERVER_PASSWORD='your-pass'
```

## Logging and Debugging

Structured logs include per-step orchestration details:
- objective extraction start/end
- discovery worker activity
- per-entity detail extraction
- per-exposure connection mapping
- unresolved and warning generation
- high-level progress lines with `%`, phase name, progress bar, and a tip (`component=progress`)

Controls:
- `--verbose` => debug logs
- `--trace` => maximum logs
- `--log-file` => append logs to file
- `DIFFMIND_LOG_LEVEL=info|debug|trace`
- `--opencode-timeout-seconds` => per-request timeout to OpenCode (default 90)
- `--cleanup-opencode-sessions` => optional session deletion; default `false` to avoid OpenCode FK race conditions
- `--opencode-delete-delay-seconds` => delay before deleting sessions when cleanup is enabled

## Output

Artifacts are written to:
- `.diffmind/runs/<run_id>/run_manifest.json`
- `.diffmind/runs/<run_id>/exposures/*.json`
- `.diffmind/runs/<run_id>/dependencies/*.json`
- `.diffmind/runs/<run_id>/connections/*.json`
- `.diffmind/runs/<run_id>/unresolved/*.json`
