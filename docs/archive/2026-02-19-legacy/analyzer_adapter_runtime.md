# Analyzer Adapter Runtime Contract (W2)

## Goal
Introduce a stable adapter execution contract in the analyzer plane so DiffMind can run built-in and future external-tool adapters under one deterministic runtime.

## CLI
`extractor analyze` now supports:
1. `--adapters` comma-separated adapter names.
2. `--allow-missing-adapters` to continue when explicitly selected adapters are unavailable.
2. `--extractors` still works and is applied inside each selected adapter plan.

Default behavior:
1. If `--adapters` is omitted, only `builtin` runs.
2. Available adapter names include `builtin`, `gopls`, `tsserver`, and `pyright`.

## Adapter SDK
Adapter interface:
1. `Name() string`
2. `Version() string`
3. `Capabilities() []string`
4. `Probe(root string) AdapterProbe`
5. `Plan(extractorSelection string) ([]Extractor, error)`

Probe response:
1. `available` boolean
2. optional `reason`
3. `tool_path`, `tool_version`, `toolchain_sha` when available

Execution policy:
1. If `--adapters` is explicitly set and any selected adapter is unavailable, analyze fails by default.
2. Use `--allow-missing-adapters` only when degraded execution is intentionally acceptable.

## Report Extensions
Analyzer report now includes:
1. `adapters[]` selected adapter names
2. `adapter_plan[]` per adapter plan metadata:
   - `name`, `version`, `capabilities`, `available`, `reason`, `tool_path`, `tool_version`, `toolchain_sha`, `extractors`
3. `adapter_runs[]` per adapter execution metadata:
   - `name`, `version`, `tool_path`, `tool_version`, `toolchain_sha`, `extractors`, `facts_added`, `evidence_added`, `replay_key`, `run_manifest_path`, `run_manifest_sha256`

`replay_key` is deterministic from:
1. `snapshot_id`
2. adapter `name` + `version`
3. adapter `toolchain_sha`
4. sorted extractor names

## Compatibility
1. Existing `--extractors` flows remain valid.
2. Existing fact and evidence bundle schema is unchanged.
3. Existing report fields remain; new fields are additive.
