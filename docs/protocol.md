# Protocol

Protocol is the Diffmind Service Context Protocol.

This repository contains the shared Go types, JSON/YAML serialization, JSON
Schema generation, and validation logic for service-context documents.

DiffMind writes Protocol. DiffMind reads Protocol.

## Schema

Current schema:

```text
diffmind.service.v1
```

Canonical generated file:

```text
.diffmind/context/service.json
```

YAML serialization is supported as:

```text
diffmind.yaml
```

`diffmind.yaml` is not the manual source of truth. Manual hints belong in
DiffMind's `diffmind-configuration.yaml`.

## Repository Layout

```text
protocol/
  types.go       Go structs for the protocol
  validate.go    semantic validation
  codec.go       JSON/YAML encode/decode helpers
  schema.go      JSON Schema generation
```

## Install And Test

```bash
cd /path/to/eyes/protocol
go test ./...
```

The local development setup expects sibling repositories:

```text
eyes/
  protocol/
  diffmind/
  diffmind/
```

DiffMind and DiffMind currently use local module replacements:

```text
replace github.com/mohammad-safakhou/diffmind/protocol => ../protocol
```

## Core Model

Protocol separates four concepts:

| Concept | Meaning |
| --- | --- |
| Object | Semantic thing: endpoint, HTTP call, DB query, queue consumer, cache op, CLI command, activation, etc. |
| Observation | Where/how an object was detected. Source file, symbol, detector, perspective. |
| Evidence | Concrete proof. Source location, config path, AST node, runtime trace, human assertion, etc. |
| Flow | Relationship/path between objects. Ordered nodes/edges, reachability, conditions, data dependencies. |

Do not put source line numbers into object IDs. IDs should be stable semantic
identities such as:

```text
http.create_campaign
dbq.insert_campaign
httpcall.catalogue_get_campaign
```

Line numbers belong in observations and evidence.

## Minimal Document

```json
{
  "schema": "diffmind.service.v1",
  "service": {
    "id": "orders-api",
    "name": "orders-api",
    "team": "commerce"
  },
  "repository": {
    "provider": "github",
    "url": "git@github.com:company/orders-api.git",
    "branch": "master",
    "commit": "abc123",
    "dirty": false
  },
  "objects": {
    "http_endpoints": [],
    "http_calls": [],
    "db_resources": [],
    "db_queries": [],
    "queue_consumers": [],
    "queue_publishers": [],
    "rpc_endpoints": [],
    "rpc_calls": [],
    "cli_commands": [],
    "activations": [],
    "cache_operations": [],
    "config_reads": [],
    "feature_flags": []
  },
  "flows": [],
  "observations": [],
  "evidence": [],
  "metadata": {
    "generated_by": "diffmind"
  }
}
```

Generated canonical JSON should include every object array, even when empty.

## Common Object Fields

Every object embeds:

```yaml
id: http.get_orders
kind: http_endpoint
name: GET /orders
status: confirmed
confidence: high
origin: deterministic
observations:
  - obs.http.get_orders.route
evidence_refs:
  - ev.http.get_orders.route
metadata: {}
```

Important fields:

| Field | Meaning |
| --- | --- |
| `status` | `confirmed`, `proposed`, `rejected`, `stale`, `deprecated`, `unresolved`, `conflicting`. |
| `confidence` | `high`, `medium`, `low`, `unknown`. |
| `origin` | `manual`, `deterministic`, `llm`, `imported`, `runtime`, `external`. |
| `observations` | Observation IDs that detected or support the object. |
| `evidence_refs` | Evidence IDs that prove the object. |

DiffMind deterministic output should use `origin: deterministic` for generated
facts. `origin: llm` should not appear in deterministic runs.

## Example Endpoint With Evidence

```yaml
objects:
  http_endpoints:
    - id: http.get_orders
      kind: http_endpoint
      name: GET /orders
      method: GET
      path: /orders
      status: confirmed
      confidence: high
      origin: deterministic
      observations:
        - obs.http.get_orders.route
      evidence_refs:
        - ev.http.get_orders.route

observations:
  - id: obs.http.get_orders.route
    object_ref: http.get_orders
    perspective: route_registration
    location:
      file: internal/http/routes.go
      start_line: 42
      end_line: 42
      symbol: RegisterRoutes
    detector: diffmind.go.echo.route
    confidence: high

evidence:
  - id: ev.http.get_orders.route
    type: source_location
    source: code
    detector: diffmind.go.echo.route
    file: internal/http/routes.go
    start_line: 42
    end_line: 42
    symbol: RegisterRoutes
    confidence: high
```

## Flow Example

```yaml
flows:
  - id: flow.get_orders
    kind: request_flow
    entrypoint: http.get_orders
    nodes:
      - id: n1
        ref: http.get_orders
        role: entrypoint
      - id: n2
        ref: dbq.select_orders
        role: action
      - id: n3
        ref: httpcall.catalogue_get_order
        role: action
    edges:
      - from: n1
        to: n2
        reachability: must
      - from: n2
        to: n3
        reachability: conditional
        condition:
          summary: order has catalogue id
    data_dependencies:
      - id: data.order_id.to_query
        from:
          object_ref: http.get_orders
          expression: request.query.orderId
        to:
          object_ref: dbq.select_orders
          expression: orders.id
        kind: filter_flow
        confidence: high
```

## Validation

Use `Validate` for protocol validity:

```go
if err := protocol.Validate(doc); err != nil {
    return err
}
```

Use `ValidateCanonical` for stricter generated-output checks:

```go
if err := protocol.ValidateCanonical(doc); err != nil {
    return err
}
```

Canonical validation is intended for generated DiffMind output. It rejects
missing common fields and bad references that would make DiffMind graphing
ambiguous.

## Relationship To The Other Repositories

DiffMind:

- imports this module,
- builds a Protocol document from deterministic findings,
- writes `.diffmind/context/service.json`.

DiffMind:

- imports this module,
- reads Protocol documents from DiffMind run directories,
- builds the company graph from service objects, resources, calls, and flows.

Protocol itself has no UI and does not scan repositories.
