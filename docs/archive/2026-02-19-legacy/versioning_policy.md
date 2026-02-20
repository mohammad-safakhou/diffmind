# Rollback and Version Compatibility Policy

## Scope

This policy defines compatibility and rollback guarantees for:

- Parse artifacts
- Fact bundles
- Canonical intelligence bundles
- Corpus golden summaries

## Compatibility Rules

1. Parser outputs are versioned by parser version under:
   - `.diffmind/parse/<snapshot_id>/<file_hash>/<parser_version>/artifact.json`
2. Fact validation is strict:
   - Facts without evidence are rejected.
3. Canonical bundle schema is append-only for optional fields in minor updates.
4. Breaking schema changes require:
   - New explicit version field or path namespace
   - Migration notes in release/PR description
   - Golden summary refresh with reviewer approval

## Rollback Procedure

1. Revert to last known good commit.
2. Re-run pipeline with `--resume=false` to force full regeneration.
3. Re-run corpus regression:
   - `make corpus`
   - `make golden`
4. If mismatch persists, restore previous golden with:
   - `git checkout -- corpus/golden/summary.json`
5. Re-run CI checks before release.

## Golden Update Control

Golden updates are never automatic in CI.

1. Developer runs:
   - `make corpus`
   - `make golden-update`
2. Reviewer must validate semantic intent of changes.
3. PR should include rationale for delta (new analyzers, parser behavior, bug fix).

## Retry and Resume Semantics

`extractor run` supports recovery controls:

- `--retries <n>` retries transient stages (`timeout`, `transient_network`)
- `--retry-delay-ms <ms>` backoff delay
- `--resume` (default true) skips stages whose output artifacts already exist
- `--no-resume` forces stage re-execution

Run reports include stage-level attempts, durations, and error taxonomy:

- `.diffmind/run/report.json`
