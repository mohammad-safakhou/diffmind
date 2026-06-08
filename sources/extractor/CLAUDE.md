# CLAUDE.md

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

## Code map

- `internal/objectives/registry.go` — objective definitions + prompts.
- `internal/agents/pipeline.go` — orchestrator / stage sequencing.
- `internal/agents/{discovery,deterministic_discovery,sharding,grounding,detail,reexamine,connections}.go`
- `internal/ast/` + `internal/ast/framework/` — tree-sitter engine + framework detectors.
- `internal/reconcile/` — final dedup / sort / orphan-drop.
- `internal/ui/` — dashboard (Go server + SPA under `web/`).
- `cmd/diffmind/` — CLI (`run`, `retry`, `ui`).

## Invariants — do NOT regress these

1. **One canonical pipeline.** Deterministic discovery always runs and is always
   merged into LLM discovery. There are no off/observe/shadow/active modes. Do
   not reintroduce run "modes" or A/B machinery.
2. **Prompts are scoped to detected languages** (`repo_facts`). Don't feed a
   single-language repo cross-language pattern lists.
3. **Sharding is evidence-gated & candidate-clustered.** Only shard directories
   with real AST candidates; never fan an empty objective into N whole-repo
   scans. A shard scopes what it *reports*, never what it may *read*.
4. **Detail is additive.** It enriches discovered entities; it must never drop
   or re-identify one. Discovery (+reexamination) is the authority on *what
   exists*.
5. **Dedup targets the architectural fact.** db/cache collapse by
   `(resource, operation)`; genuinely distinct datastores (postgres vs dynamodb)
   are preserved — never silently merge across real databases.
6. **Deterministic facts must be high-precision.** A deterministic item is fed
   to the LLM as confirmed and merged into output; a wrong one poisons results.
   Prefer "emit nothing" over "emit a guess."

## Conventions

- Commit messages end with the `Co-Authored-By: Claude ...` trailer.
- Make focused commits (one concern each), not one mega-commit.
- Every behavior change needs a unit test; pure helpers (dedup keys, sharding,
  grounding, prompt scoping) are table-tested in their package.
- Comments explain *why*, not *what* — match the existing dense style.
