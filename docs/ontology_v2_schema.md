# Ontology v2 and Compatibility Mapping

## Purpose
This document freezes the DiffMind ontology-v2 contract used by graph artifacts and query APIs.
It is the schema/semantic baseline for milestone `W1`.

## Versioning
1. Current ontology version: `v2`.
2. Supported values in graph artifacts: `v1`, `v2`.
3. New graph builds must emit `meta.ontology_version = "v2"`.
4. Backward compatibility policy:
   - `v1` artifacts remain readable.
   - `v2` adds semantic metadata (`section`, `class`, `verification_state`) as additive fields.
   - Existing node and edge IDs stay stable.

## Section Taxonomy
Allowed sections:
1. `exposure`
2. `logic`
3. `dependencies`

## Verification State Taxonomy
Allowed verification states:
1. `verified`
2. `needs_review`
3. `disputed`
4. `inferred`

## Node Semantic Fields (v2)
Each node may include:
1. `section`
2. `class`
3. `verification_state`

Normalization defaults:
1. `endpoint`, `sensitive_surface` -> `section=exposure`
2. `queue`, `topic`, `database`, `table`, `dependency`, `build_artifact` -> `section=dependencies`
3. Other node types -> `section=logic`

## Edge Semantic Fields (v2)
Each edge may include:
1. `section`
2. `class`
3. `verification_state`

Normalization defaults:
1. `service_calls_endpoint`, `queue_delivers_to_service`, `service_exposes_sensitive_surface` -> `section=exposure`
2. `service_calls_service`, `service_depends_on_dependency`, `service_reads_db`, `service_writes_db`, `service_publishes_queue`, `dependency_owned_by`, `dependency_has_risk`, `service_has_dependency_risk` -> `section=dependencies`
3. Other edge types -> `section=logic`

## Verification-State Derivation Rules
1. `inferred=true` -> `verification_state=inferred`
2. If attributes include `verification_status` or `status`, normalize it:
   - pass/ok/verified -> `verified`
   - pending/unreviewed/needs_review -> `needs_review`
   - conflict/rejected/failed/unresolved/disputed -> `disputed`
3. Node type `conflict` defaults to `disputed`.
4. Otherwise default is `verified`.

## Compatibility Mapping from Existing Types
This mapping is additive and does not rename existing node/edge `type` values.

Representative node class mapping:
1. `service` -> `class=service`
2. `endpoint` -> `class=api_endpoint`
3. `dependency` -> `class=external_dependency`
4. `config_key` -> `class=config_key`
5. `runtime_unit` -> `class=runtime_unit`
6. `verification_decision` -> `class=verification_decision`
7. `conflict` -> `class=conflict`

Representative edge class mapping:
1. `service_calls_endpoint` -> `class=api_call`
2. `service_calls_service` -> `class=service_call`
3. `service_publishes_queue` -> `class=queue_publish`
4. `queue_delivers_to_service` -> `class=queue_consume`
5. `service_reads_db` -> `class=db_read`
6. `service_writes_db` -> `class=db_write`

## Validation Invariants
1. If `meta.ontology_version` is set, it must be in supported versions.
2. If `section` is set on node/edge, it must be in allowed section values.
3. If `section` is set on node/edge, `class` must be non-empty.
4. If `verification_state` is set on node/edge, it must be in allowed verification states.
