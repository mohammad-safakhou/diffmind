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
- **The directory the server is launched from is irrelevant.** DiffMind targets a specific working directory per session via the API; you can start the server from any path (e.g. `/opt/diffmind` in Docker, the user's home dir on a VPS, etc.).

If you do not want auth right now, run without `OPENCODE_SERVER_PASSWORD` and omit auth flags in DiffMind.

### 2) Configure provider credentials in OpenCode

Use OpenCode auth commands (interactive) before running DiffMind, for example:

```bash
opencode auth login
opencode auth list
```

### 3) Headless operation: permissions and pauses

OpenCode is normally interactive: it can ask the user to allow tool calls and to clarify ambiguous prompts. Neither is appropriate for DiffMind's headless server-driven workflow, where 16 parallel sessions process the same repository and nobody is at a TUI.

DiffMind defends against this on three layers:

1. **Read-only prompts.** Every prompt explicitly instructs the agent not to edit, create, delete, or run shell commands. Combined with the per-run isolated repo snapshot, the user's repository is never the working directory of any OpenCode session.
2. **Per-run repo snapshot.** Every DiffMind run materializes an independent copy of the target repo under a random temp directory and points OpenCode at that copy. The original repo is never touched even if an agent attempts a write.
3. **Watchdog auto-replies.** A background goroutine polls `GET /permission` and `GET /question` and:
   - **denies** any permission request that originated from a DiffMind session,
   - **rejects** any clarification question from a DiffMind session,
   - **aborts** the underlying session via `POST /session/{id}/abort` whenever a prompt times out or fails.

   The watchdog only acts on session ids it created itself, so other clients sharing the same OpenCode server are not affected.

Recommended OpenCode server config for a DiffMind-only deployment (`~/.config/opencode/opencode.json`):

```json
{
  "permission": {
    "edit": "deny",
    "bash": "deny",
    "webfetch": "allow"
  }
}
```

This makes the server reject mutating tools without ever raising a prompt; the watchdog is a belt-and-braces safety net, not the primary defense.

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
  --model-variant <variant> \
  --workers 16 \
  --max-catalog-items 80 \
  --opencode-timeout-seconds 300 \
  --reuse-opencode-session=false \
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
- `--model-variant` => pass model variant to OpenCode (`medium`, `high`, `max`, etc. depending on provider/model support)
- `--max-catalog-items` => dependency items sent per connection-mapping prompt batch (default 80)
- `--reuse-opencode-session` => reuse one OpenCode session across prompts for A/B testing (default false)
- `--cleanup-opencode-sessions` => optional session deletion; default `false` to avoid OpenCode FK race conditions
- `--opencode-delete-delay-seconds` => delay before deleting sessions when cleanup is enabled

## Output

Artifacts are written to:
- `.diffmind/runs/<run_id>/run_manifest.json`
- `.diffmind/runs/<run_id>/exposures/*.json`
- `.diffmind/runs/<run_id>/dependencies/*.json`
- `.diffmind/runs/<run_id>/connections/*.json`
- `.diffmind/runs/<run_id>/unresolved/*.json`

`unresolved` is pipeline-generated and deterministic:
- agent/runtime failures
- low-confidence entities/links
- unknown-entity references in connection mapping
- detail extraction that could not confirm a discovered candidate

## Dashboard UI

You can run a local UI to inspect run results:

```bash
go run ./cmd/diffmind ui --out .diffmind/runs --host 127.0.0.1 --port 8080
```

Then open:
- `http://127.0.0.1:8080`

The dashboard shows:
- run selector (latest first)
- manifest summary cards
- exposures/dependencies/connections/unresolved grouped by artifact file
- full JSON for each group

It auto-refreshes every 10 seconds.
