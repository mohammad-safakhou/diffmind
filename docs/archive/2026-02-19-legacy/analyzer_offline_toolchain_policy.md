# Analyzer Offline Toolchain Policy (W3)

## Goal
Enforce self-hosted offline analyzer execution by default and emit a deterministic toolchain manifest with attestation metadata.

## Offline Execution
`extractor analyze` now defaults to:
1. `--offline=true`

Behavior:
1. `--offline=true` rejects `--llm-augment`.
2. To enable LLM augmentation explicitly, operator must pass `--offline=false`.

## Toolchain Manifest
Analyzer now writes:
1. `.diffmind/analyzers/toolchain_manifest.json`

Manifest includes:
1. `policy` (`self_hosted_offline`)
2. `offline` mode
3. `snapshot_id`
4. `source_root`
5. resolved adapter metadata (`name`, `version`, `capabilities`, `extractors`, `replay_key`)
6. deterministic `attestation_sha256`

## Report Additions
Analyzer report includes:
1. `offline`
2. `toolchain_manifest_path`
3. `toolchain_manifest_sha256`

These fields are additive and backward compatible.
