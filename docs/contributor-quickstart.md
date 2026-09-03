# First contribution: a reproducible workspace

Start with synthetic repositories, without company tokens, Docker, an LLM, or
installed Python/Java frameworks. These are analysis fixtures, not deployed apps.

## Build and prepare

Install the Go version in `go.mod`, a C compiler for tree-sitter/CGO, and Git.
Node.js 24 is needed for dashboard edits; distribution checks also use Ruby.
From your checkout:

```bash
make build
sh scripts/prepare-demo.sh /tmp/diffmind-first-contribution
export DIFFMIND_HOME=/tmp/diffmind-first-contribution/workspace
./bin/diffmind doctor
./bin/diffmind ui --no-spa-rebuild
```

Choose another new directory if it exists; nothing is overwritten. The script
copies the shared acceptance fixture into three Git repositories on `master`,
with synthetic author details. It does not change global Git configuration.

Open `http://127.0.0.1:8090`, create a project, and choose **Import & build** with
local directory `/tmp/diffmind-first-contribution/repositories`.

Expected: Go `gateway` calls Python `catalog` and Java `billing`.
`status.example.test` stays external. Inspect source evidence for both internal
edges. **Update graph** should then reuse all three unchanged analyses. Commit a
source comment in `catalog`, refresh, and only that repository should reanalyze.
Operations shows durable attempts; graph comparison uses explicit saved runs.

For an agent, set the same `DIFFMIND_HOME` and launch
`/absolute/path/to/diffmind/bin/diffmind mcp --project PROJECT_ID`. Do not point
your demo agent at the default real workspace accidentally.

## Verify and contribute

```bash
make test-acceptance
make test-distribution
```

The real CLI acceptance test checks exact relationships, negative/external cases,
source evidence, HTTP/MCP queries, reuse/invalidation, retries, and offline
backup/restore. Data is temporary. See [the fixture](../testdata/company/README.md).
An empty graph is not an acceptable passing test.

| Change | Start here | Checks |
| --- | --- | --- |
| Company conventions | `diffmind pack init ./my-pack --id example.conventions` | Positive/negative fixtures, graph assertions, `make test-packs` |
| Static language extraction | `internal/extractor/detectors/languages` | Detector tests, evidence, acceptance test |
| Graph queries / agents | `internal/workspace/query`, `mcpserver` | HTTP/MCP parity, historical runs, exact IDs |
| UI / onboarding | `internal/workspace/ui/web` | `make ui-test ui-build`, browser checks |
| Storage / recovery | `store`, `ui/operations.go`, `backup` | Failure/cancellation tests, `make test-race`, recovery drill |

Reproduce gaps synthetically first. State expected facts and non-matches, then
implement precise rules, include evidence locations, and document exclusions in
the support matrix. Run `make verify` before a PR. Never put internal URLs,
company names, tokens, customer code, or workspace backups in contributions.
Follow [CONTRIBUTING](../CONTRIBUTING.md) and [pack authoring](knowledge-packs.md).

Afterward stop the demo server/agent and unset `DIFFMIND_HOME`. Keep the generated
directory for future work, or remove that exact disposable directory after
checking its path. No cleanup targets a real home or source repository.
