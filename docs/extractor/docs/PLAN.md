# DiffMind Core Engine Plan

> **Historical document.** This plan describes the removed LLM/OpenCode design.
> Use [current architecture](../../ARCHITECTURE.md) and the
> [product roadmap](../../ROADMAP.md), not the older extractor roadmaps.

## Summary
DiffMind is a Go-based extraction engine that analyses a target codebase and emits structured JSON artifacts for:
- **Exposures** — entry points where external systems/users can trigger behaviour.
- **Dependencies** — external systems/resources the codebase relies on.
- **Connections** — deterministic conditional links from each exposure to every dependency it reaches.
- **Unresolved** — low-confidence or ambiguous findings.

OpenCode is used over HTTP as the reasoning core for discovery and detail enrichment. Connections are built deterministically with no LLM involvement, using a tree-sitter AST call-graph walker.

## Architecture

### Pipeline stages

```
Stage 0   repo_facts          LLM   Compact tech-stack snapshot (languages, frameworks, config hints).
Stage 0b  ast_index           AST   Parse every source file with tree-sitter; build symbol table + call graph.
Stage 0c  infrastructure      AST   Parse config files (YAML/JSON/.env) to identify service endpoints, queues, DB connections.
Stage 1   discovery           LLM   Per-objective parallel scan to discover exposures and dependencies.
Stage 2   reexamination       LLM   Re-ask for low-confidence candidates; confirm or reject.
Stage 3   detail              LLM   Enrich each verified entity with evidence, IO contract, key actions.
Stage 4   connections         AST   BFS over call graph from each exposure's entry symbol to dependency targets.
Stage 5   reconcile           DET   Deduplicate, filter orphan connections, sort for determinism.
```

### Connection engine (tree-sitter)

- Parses all source files in a snapshot using embedded tree-sitter grammars (no Docker, no compiler).
- Supported languages: Go, Python, Java, Kotlin, C#, TypeScript, JavaScript, PHP, Ruby, Rust.
- Builds a cross-file call graph with full control-flow context (if/loop/try enclosures).
- Walks BFS from each exposure's entry symbol to any dependency target symbol.
- Per-hop conditions and repetitions are extracted from the enclosing AST nodes — no LLM required.
- Falls back to a name-based shallow matcher when the index is empty.

### LLM role
LLM is used **only** for:
1. Repo facts (language/framework discovery, config hints).
2. Exposure/dependency discovery per objective.
3. Low-confidence reexamination.
4. Detail enrichment (evidence, IO contract, source locations, key actions).

LLM is **never** used for:
- Call-graph traversal.
- Condition extraction.
- Symbol resolution.
- Artifact ID generation.

## Artifact Contract

```
run_manifest.json
exposures/<type>.json
dependencies/<type>.json
connections/<from_type>__to__<dep_type>.json
unresolved/<type>.json
```

All emitted entries include source locations, bounded evidence snippets, and deterministic content-hash IDs.

## Accuracy Rules
- Precision-first: uncertain findings go to `unresolved/`.
- Main artifacts require source-backed evidence.
- Connections include normalised condition objects (`if_guard`, `loop`, `try_block`, etc.) and per-hop call paths with file:line.
- Accuracy is measured, not assumed: `internal/eval` scores artifacts against hand-labeled fixtures (`testdata/eval/`) with per-objective precision/recall/F1, matching on `entitykey.Semantic(Loose)` so phrasing variance (orders/order, SELECT/read) does not count as a miss. Items the deterministic floor cannot recover are labeled `deterministic:false` and excluded from cheap-mode scoring.

## Performance Model
- Fresh OpenCode sessions per prompt by default to avoid context-growth costs.
- Optional per-run session reuse mode for cost measurement.
- Bounded prompt payload batches (max_catalog_items).
- Tree-sitter index runs synchronously before connection stage; takes 5–30s for a typical service.
- Deterministic content-hash IDs ensure stable artifact timelines across retries.

## Retry / Resume
- Every LLM stage writes per-item checkpoints (JSONL) as it goes.
- On retry, completed items are skipped; only the failed/pending items are re-run.
- The connection stage is always re-run from the retained snapshot on retry.

## Test Strategy
- Unit tests for ID generation, config sanitization, entity conversion.
- AST unit tests for each language grammar, walker BFS, condition derivation, method reference capture.
- Agent integration tests for discovery checkpoints, detail checkpoints, retry skipping, and AST connection correctness.
- Mock OpenCode server tests for HTTP contract handling, watchdog, and liveness.
- Deterministic-precision tests for the junk-table filter, placeholder resolution, and operation-kind inference (`internal/pipeline/precision_test.go`).
- Accuracy guardrail: `go test ./internal/eval/...` runs cheap-mode scoring (deterministic floor vs. labeled fixtures) hermetically in CI; synthetic scorer tests cover phrasing-collapse, FP/FN attribution, multi-platform datastores, and connection endpoint translation.
