# Graph-First DiffMind

## Product thesis

DiffMind should be to software architecture what OpenAPI is to HTTP APIs: the
specification and database are the product; automation is how teams avoid
maintaining every fact by hand.

The previous product hierarchy was inverted:

```text
repository -> extraction run -> artifacts -> read-only graph
```

The target hierarchy is:

```text
people + automation sources -> reviewed changes -> architecture catalog
                                                   -> graph, API, exports, diffs
```

A source repository is evidence for architecture. It is not the only source and
it is not the owner of the final graph.

## Domain language

- **Catalog** — the durable architecture source of truth.
- **Record** — an exposure, dependency, or conditional connection.
- **Automation source** — source extraction, OpenAPI, AsyncAPI, cloud
  inventory, runtime telemetry, or a custom integration.
- **Proposal** — a source's suggested additions, changes, and removals.
- **Revision** — an accepted catalog changeset.
- **Ownership** — which fields are manually curated versus managed by a source.
- **Run** — execution telemetry for one automation source, not a product object.

## Principles

1. **Manual authoring is first-class.** A valid catalog never requires a source
   repository or LLM.
2. **Automation is advisory.** Sources propose changes; users control what
   becomes canonical.
3. **Identity is semantic and durable.** Catalog IDs survive run-local ID,
   location, and formatting changes.
4. **Provenance is data.** Every field should eventually explain who or what set
   it, from which evidence, and when.
5. **Edits must not be lost.** Concurrency, merge, and ownership are core domain
   behavior, not UI details.
6. **The schema is open.** The catalog API and export should be usable without
   the DiffMind automation pipeline.

## Foundation implemented

The first graph-first slice establishes:

- a canonical `architecture.v1` document independent of runs,
- manual CRUD for exposures, dependencies, and connections,
- optimistic revision checks,
- atomic file persistence,
- `manual` versus `automation` record ownership,
- semantic run imports with durable catalog IDs,
- protection of manually owned records from later imports,
- the architecture editor as the UI landing page,
- automation runs as a secondary surface.

The file lives at `<runs-dir>/architecture.v1.json`. This is intentionally a
persistence adapter, not a commitment to JSON files as the collaboration
database.

## Migration phases

### Phase 1: canonical catalog and editor

Status: foundation implemented.

- Direct graph authoring.
- Explicit import from a completed extraction run.
- Revision conflicts and record provenance.
- Existing run graph remains available for debugging automation.

### Phase 2: proposal inbox

Runs should no longer mutate the catalog directly. Each source produces a
versioned proposal:

- additions,
- changes with before/after values,
- possible removals,
- confidence and evidence changes,
- identity conflicts and ambiguous matches.

The UI must support accept/reject per record and bulk policies by source,
service, type, confidence, and ownership.

### Phase 3: field-level ownership

Record-level manual protection is conservative but coarse. Replace it with
field-level provenance:

```text
name             manual
summary          manual
source_locations extraction:run-123
evidence         extraction:run-123
platform         cloud-inventory:scan-8
```

Imports become three-way merges against the last accepted source version.
Automation may refresh owned evidence without erasing curated descriptions.

### Phase 4: immutable history and collaboration

- Append-only changesets.
- Actor/source/reason on every accepted change.
- Rollback and compare revisions.
- Comments, review status, and approvals.
- Users, roles, service ownership, and protected records.

At this point the persistence adapter should move to SQLite for local use and
Postgres for shared deployments.

### Phase 5: workspaces and fleet graph

- Multiple catalogs/workspaces.
- Services as first-class records rather than repeated strings.
- Environment overlays for dev/staging/prod instances.
- Cross-service matching: outbound HTTP/RPC/queue dependencies to inbound
  exposures.
- Architecture views filtered by team, domain, service, environment, or tag.

### Phase 6: automation platform

The current source extractor becomes one provider behind a common contract:

```text
Discover(context) -> Proposal
```

Other providers:

- OpenAPI and AsyncAPI import,
- Kubernetes/Terraform/cloud inventory,
- runtime traces and service maps,
- database catalogs,
- CI plugins and custom organization rules.

Scheduling, credentials, cost controls, and run telemetry belong to the source
provider layer. They should not leak into catalog identity or editing.

## Consequences for extraction work

Extraction accuracy still matters, but its success criteria change:

- proposal precision and recall,
- percentage accepted without edits,
- manual records incorrectly challenged,
- stable semantic matching across runs,
- evidence freshness,
- cost per accepted catalog change.

Raw run counts are even less meaningful in the graph-first product. The
important question is whether automation produces a small, trustworthy,
reviewable change to the durable architecture.
