# Contributing

Thank you for improving DiffMind.

Start with the [reproducible contributor quickstart](docs/contributor-quickstart.md)
to explore a synthetic three-service workspace without company access.
Release maintainers should also read [distribution](docs/distribution.md).

Before opening a pull request:

1. Keep changes focused and include tests for behavior changes.
2. Run `make test`.
3. Run `make test-packs` when changing knowledge packs or extraction rules.
4. Run `make test-race` when changing ingestion, storage, the pack loader, or graph pipeline.
5. Run `make ui-build` when changing either web application.
6. Run `make ui-test` when changing either dashboard or router.
7. Run `make ui-audit` when changing frontend dependencies and `make vulncheck`
   when changing Go dependencies; the accepted reachable vulnerability count
   is zero.
8. Run `diffmind doctor` when changing installation, storage, or onboarding.
9. Do not commit repository contents, generated analysis runs, credentials, or
   organization-specific examples.

Use synthetic names and data in fixtures. New detectors should prefer precise,
source-backed facts over guesses.

The workspace dashboard's `npm test` runs both helper tests and JSX component
tests through the real fetch wrapper with an in-memory API stub. The component
runner uses [LinkeDOM](https://github.com/WebReflection/linkedom) for DOM state,
not a full browser; retain browser checks for layout, focus, navigation and TLS.
Its generated test modules are temporary and cleaned up under `node_modules`.

The [current roadmap](docs/ROADMAP.md) is the source of truth for remaining work.
Run `make verify` for comprehensive local checks. The normal Go suite includes
[the synthetic company acceptance fixture](testdata/company/README.md): it builds
the actual CLI and requires Go and Git, but no Docker, framework dependencies,
company access, or LLM. New cross-repository support should add reviewed expected
relationships and source-evidence assertions, including negative cases; do not
accept an empty graph as a passing integration test.

Organization-specific conventions normally belong in a knowledge pack, not in
the core analyzer. Every contributed pack must declare an open-source license,
use synthetic fixtures, and pass both `diffmind pack lint` and
`diffmind pack test`. See [the pack authoring guide](docs/knowledge-packs.md).

`diffmind pack init ./my-pack --id example.conventions` now scaffolds a complete
identity → declaration → graph test. Use exact dependencies/exposures with
file/line assertions, an expected full graph, and at least one negative fixture.
The official packs are tested by both `go test ./...` and `make test-packs`.
Document supported patterns and exclusions in the pack README and update the
[support matrix](docs/supported-patterns.md). Framework-specific claims should
link to upstream documentation; proprietary conventions need synthetic examples.
