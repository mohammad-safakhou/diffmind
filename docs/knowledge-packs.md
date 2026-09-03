# Knowledge packs

Knowledge packs teach DiffMind deterministic organization and framework
conventions. They are data, not executable plugins: a pack can discover files,
match repository kinds, extract YAML/JSON field paths or regular expressions,
and map the results into service identities and declared relationships. The same repository and locked
pack content always produce the same result.

## Create and verify a pack

```bash
diffmind pack init ./my-company-pack --id my-company.conventions
diffmind pack lint ./my-company-pack
diffmind pack test ./my-company-pack
diffmind pack explain ./my-company-pack/pack.yaml --repo ./path/to/service
```

`pack init` creates an identity rule, an HTTP detector, two synthetic repositories,
and an exact graph assertion. Keep fixtures
small and free of proprietary names, source, credentials, and URLs.

## Manifest

```yaml
api_version: diffmind.dev/v1alpha1
kind: KnowledgePack
id: example.helm
name: Example Helm conventions
version: 1.0.0
license: Apache-2.0
compatibility: ">=0.1.0"
priority: 10
applies_to:
  kind: service_repo
  match:
    has_file: deploy/values.yaml
ignore:
  - "**/vendor/**"
extractions:
  - name: identity
    source:
      glob: deploy/values.yaml
    strategy: field_path
    extract:
      - field: service.name
        maps_to: service_name
      - field: ingress.hosts
        maps_to: dns_aliases
resolution_rules:
  - name: company service mesh
    dependency_type: "outbound_*"
    target_pattern: "^mesh://([a-z-]+)\\.svc\\.company"
    target_service: "$1-service"
    confidence: 0.99
tests:
  - name: extracts identity
    fixture: testdata/basic
    expected:
      service_name: example-service
      aliases:
        - kind: dns
          value: example.internal
```

Supported repository kinds are `service_repo`, `infra_repo`, and `any`.
Supported strategies are `field_path` and `regex`. Built-in mappings are
`service_name`, `dns_aliases`, `http_paths`, `iam_role`,
`database_connection`, `queue_identifiers`, and `queue_ownership`.
Custom scalar metadata must use a `metadata.` prefix.

Resolution rules run before generic alias matching. The target pattern is a Go
regular expression and `target_service` may expand capture groups such as
`$1`. A rule resolves only to a service present in the current graph. If
same-priority rules choose different services for one target, the run fails
with an explicit conflict.

Generic service-identity matching applies only to HTTP, RPC and queue/publish
dependencies. Database/cache operations and workflow framework usage remain
resource facts, preventing names such as `create_offer` or `camunda` from being
guessed as service addresses. Use an explicit, tested resolution rule when an
organization convention genuinely maps one of those facts to a service.
Stub-derived RPC service names require an exact identity or hostname match;
substring matching is intentionally disabled because external SDK clients often
use generic names such as `campaign` that overlap internal repository names.
For all protocols, a short target contained by a longer registered identity is
under-specified and remains unresolved; a target may only use token matching
when it contains the complete registered identity. If a target contains nested
identities, the longest (most specific) identity wins; equally specific claims
remain an ambiguity error.

Manifests are strictly parsed. Unknown fields, invalid regexes, absolute paths,
path traversal, missing tests, and ambiguous duplicate pack IDs fail validation
or testing.

## Install and pin

```bash
# Local directory
diffmind pack install ./my-company-pack

# Git source pinned to the resolved commit
diffmind pack install https://github.com/example/diffmind-pack.git --ref v1.2.0

diffmind pack list
diffmind pack disable my-company.conventions
diffmind pack enable my-company.conventions
```

Installed content lives under `$DIFFMIND_HOME/packs` (normally
`~/.diffmind/packs`). `diffmind-packs.lock` records the source, semantic
version, resolved Git commit, content digest, enabled state, and priority.
DiffMind verifies the digest before every graph run; edited or corrupted
installed content stops the run with an explicit error.

Commit the lock file when a team shares a `DIFFMIND_HOME` configuration or
reproduce installation from the pinned source and revision in company
automation.

## Detect relationships (pack runtime 0.2.0)

Set `compatibility: ">=0.2.0"` for packs using `detectors` or `graph_tests`.
Identity-only packs remain compatible. Detectors currently apply to
`service_repo` only and support `outbound_http`, `outbound_rpc`, `queue_publish`,
and `queue_consumer`. They supplement AST extraction; they do not manufacture
entrypoint reachability, runtime calls, or call traces.

```yaml
detectors:
  - name: declared HTTP clients
    type: outbound_http
    source: {glob: service-architecture.yaml}
    strategy: field_path
    field: dependencies.http
  - name: internal RPC wrapper
    type: outbound_rpc
    source: {glob: "config/**/*.properties"}
    strategy: regex
    pattern: '(?m)^rpc\.target=(?P<target>[a-z.-]+)$'
```

Field paths traverse YAML/JSON mappings and arrays; `*` selects every mapping
value or array element, numeric components select an array index, and a terminal
sequence supplies multiple targets. Selected values must be strings (null is
ignored); objects, malformed documents and duplicate keys fail the graph job.
Multi-document YAML supplies declarations from each document; it is **not** an
environment/profile precedence engine. YAML aliases are not expanded as targets.

Regex rules use Go regular expressions and require a named `target` capture.
They match text, not syntax trees: a source-code regex may match comments or
strings. Prefer narrow configuration files, anchored patterns, and negative
fixtures; contribute an AST detector for semantics-sensitive source patterns.

Source globs support `*`, `**` (zero or more directories), and `?`. Pack `ignore`
rules apply to detectors. Selected files must be regular files, at most 2 MiB;
symlink traversal is rejected, `.git` is excluded, and more than 10,000 emitted
relationships per pack/repository fails the run. Files are never executed.

HTTP targets are literal HTTP(S) URLs or service/host names. RPC targets are
literal service/host names (optionally with ports); queues are literal names or
ARNs, not URLs. Environment/template placeholders are not evaluated. HTTP URLs
containing user info, query strings or fragments are rejected to reduce accidental
credential disclosure. Evidence includes relative file/line, pack ID/version,
detector and a declared-relationship confidence, not raw source snippets.
`pack explain` reports detections and sanitized `skipped` diagnostics.

The resolver handles aliases and resolution rules for the detected dependencies.
Equally strong aliases belonging to multiple services fail with an ambiguity
error; add an explicit rule instead of relying on registry order. Detected
relationships and HTTP/RPC resolution results are incorporated into the graph
snapshot shared by the UI and MCP. Disabling/updating a pack changes the next
graph, not earlier saved graphs or the underlying analyzer artifacts.

## Test identities, evidence and the complete graph

`tests` still asserts the exact identity. For a detector pack, also list the exact
`dependencies` and `exposures`; an omitted list means **expect none**:

```yaml
tests:
  - name: client evidence
    fixture: testdata/gateway
    expected: {service_name: gateway}
    dependencies:
      - type: outbound_http
        target: https://catalog/products
        file: service-architecture.yaml
        line: 4
graph_tests:
  - name: resolves the client
    repositories:
      - {name: gateway, fixture: testdata/gateway}
      - {name: catalog, fixture: testdata/catalog}
    edges:
      - {from: gateway, to: catalog, type: http}
```

Graph tests run **the production resolver and rich graph builder**, using only
the pack being tested plus repository identity overrides. They do not run the
analyzer or read existing analyzer artifacts. Fixtures may include repositories
that do not match the pack, to represent known destination services. Assertions
compare the entire edge set, including external services and resource edges;
empty `edges: []` is useful for a negative fixture. Graph service keys are the
fixture repository names; aliases resolve to those keys. Use graph-normalized
resource IDs, as shown in the service-manifest fixtures.

The official [service-manifest pack](../packs/service-manifest/README.md) supplies
an executable HTTP/RPC/queue example. The
[OpenFeign configuration pack](../packs/spring-openfeign-config/README.md) shows
framework configuration detection with both structured and regex strategies.
Both require explicit installation; they are not automatically enabled.
See the [tested support matrix](supported-patterns.md) before assuming coverage.

## Repository override

A repository can take explicit ownership of its canonical identity:

```yaml
# .diffmind/service.yaml
api_version: diffmind.dev/v1alpha1
kind: ServiceIdentity
service_name: checkout-service
aliases:
  - kind: dns
    value: checkout.internal
metadata:
  owner: commerce
```

This file has higher precedence than every pack. It is useful for exceptions;
repeatable organization conventions should remain in a tested pack.

## Contributing a pack

Put generally useful packs under `packs/<id>/`. Every official pack needs:

- a semantic version and open-source license;
- synthetic fixtures for every supported convention;
- deterministic expected identities and file/line detection assertions;
- positive and negative fixtures, plus exact graph tests for relationships;
- documented supported patterns, exclusions, and framework references;
- no company, customer, credential, or private infrastructure data.

CI lints and runs every official pack in addition to the Go test suite.
