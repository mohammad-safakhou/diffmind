# Contributing

Thank you for improving DiffMind.

Before opening a pull request:

1. Keep changes focused and include tests for behavior changes.
2. Run `make test`.
3. Run `make test-packs` when changing knowledge packs or extraction rules.
4. Run `make test-race` when changing the pack loader or graph pipeline.
5. Run `make ui-build` when changing either web application.
6. Run `make ui-test` when changing the extractor dashboard or router.
7. Run `diffmind doctor` when changing installation, storage, or onboarding.
8. Do not commit repository contents, generated analysis runs, credentials, or
   organization-specific examples.

Use synthetic names and data in fixtures. New detectors should prefer precise,
source-backed facts over guesses.

Organization-specific conventions normally belong in a knowledge pack, not in
the core analyzer. Every contributed pack must declare an open-source license,
use synthetic fixtures, and pass both `diffmind pack lint` and
`diffmind pack test`. See [the pack authoring guide](docs/knowledge-packs.md).
