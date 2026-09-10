# Public compatibility benchmarks

These benchmarks make DiffMind's framework coverage measurable without using
private code. They complement the curated [Demo Shop](../demo-shop/README.md):
Demo Shop is the stable product story; these larger projects are evolving gap
finders and contribution targets.

## Pinned sources

| Project | Revision tested | License | Why it is useful |
| --- | --- | --- | --- |
| [OpenTelemetry Demo](https://github.com/open-telemetry/opentelemetry-demo) | `147ddb4fefe4978083bbaeb4d1efb14946c465ab` | Apache-2.0 | Broad language and telemetry stack |
| [Google Online Boutique](https://github.com/GoogleCloudPlatform/microservices-demo) | `b9a978db9e01f4ad3dca9494a22cb9edc17548fe` | Apache-2.0 | Well-known polyglot gRPC system |

Clone the exact revisions and analyze every `src/*` directory independently,
followed by the complete monorepo:

```bash
make build
sh scripts/prepare-public-benchmarks.sh /tmp/diffmind-public-benchmarks
sh scripts/analyze-public-monorepo.sh \
  /tmp/diffmind-public-benchmarks/opentelemetry-demo \
  /tmp/diffmind-otel-results
sh scripts/analyze-public-monorepo.sh \
  /tmp/diffmind-public-benchmarks/online-boutique \
  /tmp/diffmind-online-boutique-results
```

The analyzer scopes a nested monorepo run to the requested directory. This is
important: running it on `src/checkout` must not silently include sibling
services. The whole-repository run remains useful for language/framework
inventory, but it is not equivalent to a multi-repository workspace graph.

## Verification record — 10 September 2026

All 44 independent `src/*` scans and both whole-monorepo scans completed without
an analyzer error at the pinned revisions.

| Dataset | Independent targets | Targets with architecture entities | Exposures | Dependencies | Connections |
| --- | ---: | ---: | ---: | ---: | ---: |
| OpenTelemetry Demo | 32 | 4 | 15 | 2 | 0 |
| Google Online Boutique | 12 | 6 | 7 | 2 | 0 |

The complete OpenTelemetry run reported repository metrics for 366 files across
15 recorded languages and found 15 exposures and 2 dependencies. The complete
Online Boutique run reported metrics for 170 files across 8 recorded languages
and found 7 exposures and 2 dependencies. These numbers are observations for
the pinned commits, not a promise of semantic completeness.

The zero connection count is the most useful result: both projects depend
heavily on protobuf/gRPC and configuration-driven service identities that the
current deterministic detectors do not yet join reliably. Priority contribution
areas are protobuf service/field extraction, gRPC server and client detection in
Go/Java/C#/Python/Node, environment-variable endpoint resolution, and
framework-specific message/config conventions. New support should arrive with
small positive and negative fixtures first, then be checked against these pinned
projects. Do not add broad regex rules merely to increase benchmark counts.

## Agent-assisted contribution loop

An agent can use the results without being allowed to invent architecture:

1. Pick one missing relationship with clear source and configuration evidence.
2. Reduce it to a synthetic fixture that contains no upstream or company data.
3. Decide whether it belongs in a knowledge pack or requires AST semantics.
4. Add positive, negative and ambiguity tests before implementing support.
5. Run unit tests, `make test-showcase`, then the affected pinned benchmark.
6. Report precision evidence and known false-negative cases in the pull request.

Generated benchmark artifacts and cloned repositories belong outside Git. They
may contain many upstream files and absolute local paths; commit only sanitized
fixtures, detector changes and aggregate verification notes.
