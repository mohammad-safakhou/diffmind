# diffmind-configuration.yaml

`diffmind-configuration.yaml` is the only manual, repository-local extension
file for deterministic DiffMind.

Use it to teach DiffMind company-specific naming and wiring conventions. Do not
use it to list every endpoint or dependency by hand. DiffMind should discover
source-code facts; configuration should resolve ambiguity.

## Location

Put the file at the root of the analyzed service repository:

```text
service-repo/
  diffmind-configuration.yaml
```

## Minimal File

```yaml
schema: diffmind.config.v1

service:
  id: orders-api
  name: orders-api
  team: commerce
```

All sections are optional, but `schema: diffmind.config.v1` is recommended.

## Top-Level Shape

```yaml
schema: diffmind.config.v1

service: {}
paths: {}
aliases: {}
http_targets: []
resource_patterns: []
config: {}
conventions: {}
detectors: {}
patterns: []
```

## service

Overrides metadata for the service.

```yaml
service:
  id: gateway-service
  name: gateway-service
  team: cfp
  domain: marketing
  criticality: high
```

Use this when repository names, package names, or catalog metadata do not give a
clean service identity.

## paths

Controls which files are analyzed.

```yaml
paths:
  include:
    - "src/**"
    - "cmd/**"
  exclude:
    - "vendor/**"
    - "node_modules/**"
    - "dist/**"
    - ".diffmind/**"
    - ".diffmind/**"
```

DiffMind already ignores common generated/vendor/binary directories. Use this
only for repository-specific source layout.

## aliases

Aliases map many company names to one canonical identity.

```yaml
aliases:
  services:
    checkout-service:
      - catalogue_api
      - catalog-api
      - checkout-service.default.svc.cluster.local
  resources:
    traffic-info:
      - TRAFFIC_INFO_TABLE
      - traffic_info
      - arn:aws:dynamodb:*:*:table/traffic-info
```

Rules:

- Use canonical names as map keys.
- Put historical names, DNS names, env var names, and generated client names as
  aliases.
- Prefer aliases over hardcoded detector behavior.

## http_targets

Resolves outbound HTTP clients to service refs or external services.

```yaml
http_targets:
  - id: catalogue-client
    service_ref: service.checkout-service
    client_class: CatalogueManagementApiClient
    config_key: services.catalogue.url
    url_host: checkout-service.default.svc.cluster.local
    path_prefix: /campaigns
    aliases:
      - catalogue_api
```

Fields:

| Field | Meaning |
| --- | --- |
| `id` | Stable config rule id. Required. |
| `service_ref` | Protocol service ref, usually `service.<service-id>`. Required. |
| `external` | Mark target as external when it is not a company service. |
| `client_class` | Client/interface/class name to match. |
| `config_key` | Config property/env key that contains the base URL. |
| `url_host` | Hostname to match after URL parsing. |
| `path_prefix` | URL path prefix that identifies the target. |
| `aliases` | Extra target names. |
| `metadata` | Optional labels. |

Example for external APIs:

```yaml
http_targets:
  - id: openai
    service_ref: service.openai
    external: true
    client_class: OpenAIClient
    url_host: api.openai.com
```

## resource_patterns

Resolves databases, caches, queues, topics, streams, and other resources.

```yaml
resource_patterns:
  - id: orders-postgres
    kind: database
    platform: postgres
    resource_ref: db.orders-db
    config_key: spring.datasource.url
    aliases:
      - ORDERS_DATABASE_URL

  - id: traffic-info-redis
    kind: cache
    platform: redis
    resource_ref: cache.traffic-info
    config_key: TRAFFIC_INFO_REDIS_URL

  - id: catalogue-events
    kind: queue
    platform: sqs
    resource_ref: queue.catalogue-events
    name_pattern: "catalogue-.*-events"
```

Fields:

| Field | Meaning |
| --- | --- |
| `id` | Stable config rule id. Required. |
| `kind` | `database`, `cache`, `queue`, `topic`, `stream`, etc. Required. |
| `platform` | `postgres`, `redis`, `sqs`, `kafka`, `dynamodb`, etc. |
| `resource_ref` | Protocol resource ref. |
| `config_key` | Config/env key that names the resource. |
| `url_host` | Hostname to match. |
| `name_pattern` | Regex/string pattern for resource names. |
| `aliases` | Additional names. |
| `metadata` | Optional labels. |

## config

Adds company-specific config file locations and env/profile mappings.

```yaml
config:
  paths:
    - "application*.yml"
    - ".example/config/**/values.yaml"
    - "deploy/**/values.yaml"
  profiles:
    prod: production
    stage: staging
  env:
    SERVICE_BASE_URL: services.orders.url
```

Use this when important URLs, queues, or database names are hidden in custom
configuration directories.

## conventions.dependency_injection

Describes wiring conventions that reveal dependencies. This is useful for Go
Wire-style projects where the dependency graph is visible in provider sets.

```yaml
conventions:
  dependency_injection:
    - id: service-wire
      kind: go_wire
      roots:
        - "internal/infra/**"
        - "internal/wire/**"
      sets:
        application: "ApplicationSet"
        infrastructure: "InfrastructureSet"
      entrypoints:
        http: "HTTPServer"
        grpc: "GRPCServer"
      classifications:
        - match:
            type: "OpenAIClient"
          target_ref: service.openai
          kind: http_call
          config_keys:
            - OPENAI_API_KEY
            - OPENAI_BASE_URL
```

Supported `kind` values today:

- `go_wire`
- `wire`

## detectors

Detector selection is mostly automatic. DiffMind runs all relevant detectors for
the detected languages.

```yaml
detectors:
  disabled:
    - go.http.echo
  options:
    helm_values_glob: ".example/config/**/values.yaml"
```

Do not use deprecated `enabled` for normal repositories. If a detector is noisy,
disable it with a specific reason in code review.

## patterns

Declarative regex patterns for custom conventions. These are for simple,
repeatable cases where writing a Go detector is not justified yet.

```yaml
patterns:
  - id: custom-webhook-route
    kind: http_endpoint
    language: go
    file_glob: "internal/**/*.go"
    regex: 'RegisterWebhook\("(?P<path>[^"]+)",\s*(?P<handler>[A-Za-z0-9_.]+)\)'
    fields:
      method: POST
      path: "$path"
      handler: "$handler"
    description: "Company webhook router helper"
```

Regex must compile. Keep patterns narrow and evidence-backed.

## Good Configuration Style

Good:

```yaml
http_targets:
  - id: catalogue-client
    service_ref: service.checkout-service
    client_class: CatalogueManagementApiClient
```

Bad:

```yaml
patterns:
  - id: every-catalogue-call
    regex: ".*catalogue.*"
```

Guidelines:

- Resolve names, do not invent facts.
- Prefer service/resource aliases over broad regex.
- Keep custom patterns narrow.
- Document company-specific conventions in `description` or `metadata`.
- Do not put generated Protocol content in this file.

## Validation Errors

Common validation failures:

| Error | Fix |
| --- | --- |
| `schema ... unsupported` | Use `schema: diffmind.config.v1`. |
| `http_targets[n].id is required` | Add a stable `id`. |
| `http_targets[n].service_ref is required` | Add `service.<name>` target ref. |
| `resource_patterns[n].kind is required` | Add `database`, `cache`, `queue`, etc. |
| `patterns[n].regex` | Fix invalid regex syntax. |
| `unknown detector` | Remove old detector IDs or use current registry IDs. |
