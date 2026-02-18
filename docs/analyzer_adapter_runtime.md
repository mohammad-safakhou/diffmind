# Analyzer Adapter Runtime Contract (W2)

## Goal
Introduce a stable adapter execution contract in the analyzer plane so DiffMind can run built-in and future external-tool adapters under one deterministic runtime.

## CLI
`extractor analyze` now supports:
1. `--adapters` comma-separated adapter names.
2. `--extractors` still works and is applied inside each selected adapter plan.

Default behavior:
1. If `--adapters` is omitted, all built-in adapters run.
2. Current built-in adapter name: `builtin`.

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

## Report Extensions
Analyzer report now includes:
1. `adapters[]` selected adapter names
2. `adapter_plan[]` per adapter plan metadata:
   - `name`, `version`, `capabilities`, `available`, `reason`, `extractors`
3. `adapter_runs[]` per adapter execution metadata:
   - `name`, `version`, `extractors`, `facts_added`, `evidence_added`, `replay_key`

`replay_key` is deterministic from:
1. `snapshot_id`
2. adapter `name` + `version`
3. sorted extractor names

## Compatibility
1. Existing `--extractors` flows remain valid.
2. Existing fact and evidence bundle schema is unchanged.
3. Existing report fields remain; new fields are additive.
