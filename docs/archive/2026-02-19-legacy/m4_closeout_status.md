# M4 Closeout Status

Generated on: 2026-02-19

Primary evidence artifact:
- `.diffmind/m4-closeout/m4_closure_report.json`

Contract baseline:
- `.diffmind/m4-closeout/m4_expected_contract.json`

## Verification Scope
1. M4.1 adapter runtime with non-builtin adapter execution.
2. M4.1 replay parity (same snapshot/tool inputs).
3. M4.2 Java outbound extraction precision (false-positive suppression).
4. M4.2 queue target resolution via property-backed bindings.
5. M4.2 default exclusion of `src/test/**` architecture signals.
6. M4.3 Spring profile merge + code-reference resolution.

## Commands Used
```bash
# Full checkout-service M4 run
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor scan --source ./checkout-service --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor parse --source ./checkout-service --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor analyze --source ./checkout-service --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor bundle --in .diffmind/m4-closeout/analyzers/bundle.json --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor graph build --mode single --service-id checkout-service --service-name checkout-service --bundle .diffmind/m4-closeout/bundle/intelligence_bundle.json --analyzer-bundle .diffmind/m4-closeout/analyzers/bundle.json --out .diffmind/m4-closeout
GOCACHE=$(pwd)/.gocache go run ./cmd/extractor graph assess --index .diffmind/m4-closeout/graph/index.json --out .diffmind/m4-closeout/graph/merge_quality_report.json --fail-on-gate

# Non-builtin adapter + replay parity evidence
DIFFMIND_GOPLS_BIN=.diffmind/m4-closeout/bin/gopls GOCACHE=$(pwd)/.gocache go run ./cmd/extractor analyze --source . --out .diffmind/m4-closeout/adapter-runs/run1 --snapshot-id m4-replay-001 --adapters gopls --extractors runtime
DIFFMIND_GOPLS_BIN=.diffmind/m4-closeout/bin/gopls GOCACHE=$(pwd)/.gocache go run ./cmd/extractor analyze --source . --out .diffmind/m4-closeout/adapter-runs/run2 --snapshot-id m4-replay-001 --adapters gopls --extractors runtime
```

## Current Result
- M4 closure status: **PASS** (`overall_passed=true`) per `.diffmind/m4-closeout/m4_closure_report.json`.
