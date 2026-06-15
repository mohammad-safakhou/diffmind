# Backend Architecture

DiffMind's backend follows a one-way dependency flow:

```text
cmd / ui
   -> app / runner
   -> pipeline
   -> floor / stage packages
   -> extraction contracts and focused services
```

## Package Responsibilities

- `internal/extraction`: candidates, repo facts, pipeline result/failure DTOs,
  semantic candidate rules, prompt contracts, schemas, and path mapping.
- `internal/pipeline`: lifecycle, snapshot ownership, stage sequencing,
  progress/events, cancellation, resume decisions, and terminal assembly.
- `internal/floor`: eval-facing deterministic projection of the LLM-free path.
  This is not a runtime mode.
- `internal/stage/*`: stage-owned deterministic or LLM work. Stage packages
  never import the pipeline or another stage package.
- `internal/llmrun`: sessions, watchdog/liveness, prompt runtime capabilities,
  token reads, and provider error classification.
- `internal/runstate`: backward-compatible checkpoint files and state paths.
- `internal/entitykey`: canonical architectural identities shared by extraction,
  reconciliation, and evaluation.
- `internal/opencode`: OpenCode HTTP transport and wire decoding.

## Dependency Rules

1. A stage exposes typed `Input` and `Output` values and a concrete `Runner`.
2. Stages do not import `internal/pipeline` or another `internal/stage/*`.
3. Shared data belongs in `extraction`; transport behavior belongs in
   `llmrun`; persisted state belongs in `runstate`.
4. Do not add `core`, `common`, `shared`, or catch-all utility packages.
5. CLI, HTTP, artifacts, events, IDs, checkpoint filenames, and retry readers
   are compatibility contracts.

`internal/architecture/imports_test.go` enforces the stage import rules.

## Pipeline

```text
repo_facts -> ast_index -> deterministic discovery + LLM discovery
           -> reexamination -> deterministic entity conversion/backfills
           -> connections -> connection repair -> reconcile
```

There is one canonical path. Deterministic discovery always runs and merges
into LLM discovery. Discovery owns entity identity and richness; there is no
later LLM detail stage. Reconciliation uses `entitykey.Semantic` as the
canonical identity.
