# AGENTS.md

Guidance for AI coding sessions working in this repo. **Read `docs/PLATFORM.md`
first** for the product vision, design rationale, pipeline, and roadmap — this
file is the operational quick-reference.

## What this is (one line)

A multi-language architecture extractor: it reads a service's source and emits
exposures, dependencies, and their conditional connections as JSON. The LLM does
the semantic understanding; deterministic static analysis (tree-sitter AST) is
the recall floor, the LLM's context, and the verifier.

## Build / test / run

```bash
go build ./...          # must stay green
go test ./...           # must stay green before any commit
go build -o diffmind ./cmd/diffmind    # rebuild the binary before a real run
```

Run an extraction (needs a running OpenCode server — see README §"OpenCode
Setup"):

```bash
go run ./cmd/diffmind run --repo /abs/path/to/target --opencode-url http://127.0.0.1:4096 ...
```

Artifacts + logs for every run land in `~/.diffmind/runs/<run_id>/`
(`run_manifest.json`, `exposures/`, `dependencies/`, `connections/`,
`state/`, `events.jsonl`, `prompts/`). Diffing successive runs there is the
primary way to judge a behavior change.

Measure accuracy against hand-labeled fixtures (no OpenCode needed for cheap
mode — it scores the deterministic floor only):

```bash
go test ./internal/eval/...                                  # hermetic CI guardrail (cheap mode)
go run ./cmd/diffmind eval --mode cheap --fixtures testdata/eval   # per-objective P/R/F1 table
go run ./cmd/diffmind eval --mode score-run --run <run-id> --fixture testdata/eval/<name>  # grade a real run
```

`SemanticKey`/`SemanticKeyLoose` (reconcile) are the shared identity the matcher
uses, so "correct" is judged exactly as the pipeline judges "duplicate". See
`testdata/eval/README.md` for the label format.

## Code map

- `internal/objectives/registry.go` — objective definitions + prompts.
- `internal/pipeline/` — extraction lifecycle, stage sequencing, resume/failure
  routing, progress/events, snapshot ownership, and terminal result assembly.
- `internal/stage/` — typed stage packages (`repofacts`, `astindex`,
  `discovery`, `reexamine`, `connections`, `reconcile`).
- `internal/floor/` — LLM-free projection of the deterministic path; powers
  cheap-mode eval.
- `internal/stage/discovery/config_resolve.go` — `${...}` property-placeholder
  resolver (queue/topic names) against the parsed config index.
- `internal/llmrun/` — prompt execution, sessions, retries, watchdog/liveness,
  captures, token accounting, and provider error classification.
- `internal/runstate/` — checkpoints, stage state, failure reports, and
  backward-compatible readers.
- `internal/entitykey/` — canonical identities shared by reconcile and eval.
- `internal/ast/` + `internal/ast/framework/` — tree-sitter engine + framework detectors.
- `internal/reconcile/` — final dedup / sort / orphan-drop; `SemanticKey(Loose)`
  is the exported identity the eval matcher reuses.
- `internal/eval/` — golden-set accuracy harness (label loader, identity keying,
  P/R/F1 scorer, cheap + score-run modes). Fixtures under `testdata/eval/`.
- `internal/ui/` — dashboard (Go server + SPA under `web/`).
- `cmd/diffmind/` — CLI (`run`, `retry`, `validate`, `list-runs`, `eval`, `ui`).

## Invariants — do NOT regress these

1. **One canonical pipeline.** Deterministic discovery always runs and is always
   merged into LLM discovery. There are no off/observe/shadow/active modes. Do
   not reintroduce run "modes" or A/B machinery.
2. **Prompts are scoped to detected languages** (`repo_facts`). Don't feed a
   single-language repo cross-language pattern lists.
3. **Sharding is evidence-gated & candidate-clustered.** Only shard directories
   with real AST candidates; never fan an empty objective into N whole-repo
   scans. A shard scopes what it *reports*, never what it may *read*.
4. **Discovery owns entities.** Discovery (+reexamination) is the authority on
   what exists, its identity, and its semantic richness. Deterministic
   conversion/backfills may enrich an entity but must never drop or re-identify
   one. Do not reintroduce an LLM detail stage.
5. **Dedup targets the architectural fact.** db/cache collapse by
   `(resource, operation)`; genuinely distinct datastores (postgres vs dynamodb)
   are preserved — never silently merge across real databases.
6. **Deterministic facts must be high-precision.** A deterministic item is fed
   to the LLM as confirmed and merged into output; a wrong one poisons results.
   Prefer "emit nothing" over "emit a guess."

## Conventions

- Commit messages end with the `Co-Authored-By: Codex ...` trailer.
- Make focused commits (one concern each), not one mega-commit.
- Every behavior change needs a unit test; pure helpers (dedup keys, sharding,
  grounding, prompt scoping) are table-tested in their package.
- Comments explain *why*, not *what* — match the existing dense style.
