# Knowledge packs

Knowledge packs teach DiffMind deterministic organization and framework
conventions. They are data, not executable plugins: a pack can discover files,
match repository kinds, extract YAML/JSON field paths or regular expressions,
and map the results into a service identity. The same repository and locked
pack content always produce the same result.

## Create and verify a pack

```bash
diffmind pack init ./my-company-pack --id my-company.conventions
diffmind pack lint ./my-company-pack
diffmind pack test ./my-company-pack
diffmind pack explain ./my-company-pack/pack.yaml --repo ./path/to/service
```

`pack init` creates a manifest and a synthetic test repository. Keep fixtures
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
- deterministic expected identities;
- no company, customer, credential, or private infrastructure data.

CI lints and runs every official pack in addition to the Go test suite.
