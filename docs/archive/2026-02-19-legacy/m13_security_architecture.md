# M13 Security And Compliance Architecture

## Scope
This document describes the M13 implementation for enterprise security and compliance in DiffMind.

## 1. Tenant Isolation + RBAC/ABAC
- Auth context is extracted from HTTP headers:
  - `X-DiffMind-Tenant`
  - `X-DiffMind-Principal`
  - `X-DiffMind-Roles`
  - `X-DiffMind-Scopes`
  - optional `X-DiffMind-Attr-*` attributes
- Tenant isolation is enforced in policy decisions and graph ownership:
  - Graph artifacts carry `meta.tenant_id`.
  - Graph index carries `tenant_id` in summaries.
  - Cross-tenant compare is rejected.
- RBAC/ABAC policy engine (`internal/security/policy.go`) defines action-based authorization.

## 2. Redaction Policy
- Sensitive fields are redacted unless caller has explicit permission.
- Redaction applies to graph response attributes and evidence metadata.
- Secret-like keys and values are masked (`[REDACTED]`).
- Raw evidence paths/line locations require elevated scope (`evidence:raw`) or admin role.

## 3. Encryption + Key Management Standards
- Compliance audit export supports optional AES-256-GCM encryption.
- Key material is provided via:
  - `DIFFMIND_AUDIT_EXPORT_KEY_B64` (base64-encoded 32-byte key)
  - `DIFFMIND_KMS_KEY_ID` (key identifier metadata)
- Export manifests support optional HMAC signatures:
  - `DIFFMIND_AUDIT_MANIFEST_HMAC_KEY_B64` (base64 key for HMAC-SHA256 manifest signing)
  - `DIFFMIND_AUDIT_MANIFEST_KEY_ID` (signature key identifier metadata)

## 4. Audit Logging
- Full audit event logging is implemented for authorized/denied operations on query and mutating endpoints.
- Events are persisted as JSONL at `.diffmind/audit/events.jsonl` (or corresponding output root).
- Event schema includes action, tenant, principal, path, method, decision, and reason.

## 5. Retention + Compliance Export Workflows
- New API endpoints:
  - `GET /compliance/audit` (read events)
  - `GET /compliance/audit/integrity` (verify tamper-evident audit chain)
  - `POST /compliance/audit/export` (export audit events, optional encryption)
  - `POST /compliance/audit/export/verify` (verify export manifest digest/signature)
  - `GET /compliance/audit/evidence-bundle` (list bundles or read one by `path`)
  - `POST /compliance/audit/evidence-bundle` (generate persisted security evidence bundle artifact)
  - `POST /compliance/audit/retention` (prune events by retention days)
- Retention pruning is tenant-scoped under authorization.
- Evidence bundles are checksum-protected (`checksum_sha256`) and validated on retrieval.

## 6. Verification
- Unit/integration tests added for:
  - authorization behavior and tenant isolation
  - sensitive redaction
  - audit append/list/prune/export
  - compliance API endpoints
- Full repository test suite passes with M13 changes.
