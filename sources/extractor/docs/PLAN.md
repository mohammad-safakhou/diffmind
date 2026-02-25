# DiffMind Core Engine Plan

## Summary
DiffMind is a Go-based extraction engine that analyzes a target codebase and emits structured JSON artifacts for:
- Exposures: entrypoints where external systems/users can trigger behavior.
- Dependencies: external systems/actions the codebase relies on.
- Connections: conditional direct links from exposure to dependency.
- Unresolved: low-confidence or conflicting findings.

OpenCode is integrated over HTTP and used as the primary analyzer/reasoning core for discovery, extraction, and mapping with source-backed evidence.

## Architecture
1. CLI runtime (`diffmind run`) triggers one extraction run.
2. OpenCode-driven discovery agent finds exposures.
3. OpenCode-driven discovery agent finds dependencies.
4. OpenCode-driven mapper agent builds conditional exposure->dependency links.
5. DiffMind validates confidence/evidence and materializes unresolved items.
7. Artifact writer emits versioned JSON outputs to `.diffmind/runs/<run_id>/`.

## Artifact Contract
- `run_manifest.json`
- `exposures/<type>.json`
- `dependencies/<type>.json`
- `connections/<from>__to__<to>.json`
- `unresolved/<type>.json`

All emitted entries include source locations and bounded evidence snippets.

## Accuracy Rules
- Precision-first gate: uncertain findings go to `unresolved/*`.
- Main artifacts require source-backed evidence.
- Connections include normalized condition objects and human explanation.

## Extensibility
A plugin SDK surface is represented in core interfaces and category/tag schema fields so custom extractors can be added without breaking consumer contracts.

## Performance Model
- OpenCode session reuse per run to keep context/tool state warm.
- Bounded prompt passes for discovery and mapping.
- Deterministic content-hash IDs.

## Test Strategy
- Unit tests for ID generation.
- Integration tests for full run + artifact emission.
- Mock OpenCode server tests to verify HTTP contract handling.
