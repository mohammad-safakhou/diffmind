# M13 Security Architecture (Active Summary)

## Implemented Security Surface

1. Central authorization policy in `internal/security/policy.go`.
2. Tenant-aware authorization + audit logging in HTTP handlers.
3. Compliance/audit endpoints under `/compliance/audit*`.
4. Evidence redaction controls for sensitive payloads.
5. Token auth context extraction in `internal/security/types.go` + `internal/security/jwt.go`.

## Current Auth Model

1. `DIFFMIND_AUTH_MODE=header` (default): API auth context is derived from `X-DiffMind-*` headers.
2. `DIFFMIND_AUTH_MODE=jwt`: `Authorization: Bearer <token>` is required and verified.
3. `DIFFMIND_AUTH_MODE=auto`: JWT is preferred when bearer is present; otherwise header mode is used.
4. JWT claim mapping supports tenant/principal/roles/scopes/attrs claim configuration via `DIFFMIND_AUTH_*` env vars.
5. HS256 and RS256 signature verification are supported.
6. RS256 supports OIDC discovery + JWKS key retrieval/rotation:
   `DIFFMIND_AUTH_JWT_OIDC_ISSUER`, `DIFFMIND_AUTH_JWT_JWKS_URL`, `DIFFMIND_AUTH_JWT_JWKS_CACHE_SECONDS`.
7. IdP claim contract templates are supported via `DIFFMIND_AUTH_PROFILE`:
   `custom`, `keycloak`, `entra`, `cognito`.
8. Nested claim path mapping is supported for JWT claims (example: `realm_access.roles`).

## Remaining Security Work

1. None in active scope.

Legacy detailed document: `docs/archive/2026-02-19-legacy/m13_security_architecture.md`.
