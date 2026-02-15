# Graph Performance Baseline

## Benchmark

Run:

- `go test ./internal/graph -bench BenchmarkBuildGraphMediumFixture -benchmem`

This benchmark builds a multi-service graph from a synthetic medium fixture containing runtime units, endpoints, HTTP calls, queue calls, and DB calls.

## Baseline Policy

- Track benchmark output in PRs for notable graph/resolver changes.
- Regressions should be investigated if median runtime increases by >20% over recent local baseline on same machine.
- CI runs correctness tests (`go test ./...`); benchmark tracking is informational for now.
