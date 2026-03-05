# Security

**Source:** `internal/middleware/auth.go`, `internal/middleware/grpc_chain.go`, `internal/middleware/http_stack.go`, `internal/middleware/security_headers.go`, `internal/middleware/request_size_limit.go`, `internal/middleware/ratelimit.go`, `internal/config/types.go`

## Authentication

**Mechanism:** Static admin key or trusted header forwarding (not JWT/OAuth).

| Mode | Behavior |
|---|---|
| `auth.enabled: false` | All requests bypass auth. Missing `X-Tenant-ID` falls back to `"default-tenant"`. Intended for local development only. |
| `auth.enabled: true` + `auth.admin_key` configured | Requests with `Authorization: Bearer <admin_key>` pass auth. Tenant defaults to `"system-admin"` if `X-Tenant-ID` is absent. |
| `security.trust_tenant_id_from_request: true` | The `X-Tenant-ID` header is trusted as the tenant identity (Linkerd mTLS mesh enforces authenticity at the network layer). |

### gRPC Interceptor Chain (Ordered)

**Source:** `internal/api/grpc/server.go`, `internal/middleware/grpc_chain.go`

1. **RecoveryInterceptor** — Catches panics.
2. **DeadlineInterceptor** — Enforces a 30s default timeout.
3. **RequestIDInterceptor** — Extracts/generates UUID request ID.
4. **GRPCRateLimitInterceptor** — Enforces per-tenant rate limits.
5. **ContextLoggerInterceptor** — Attaches logger with `request_id`.
6. **LoggerInterceptor** — Logs every RPC with `method`, `code`, `latency`, `tenant_id`.
7. **AuthInterceptor** — Extracts `tenant_id` from metadata into context.
8. **EnforceTenantInterceptor** — Rejects requests if tenant context is missing.
9. **ValidationInterceptor** — Calls `req.Validate()` on request messages.

> [!IMPORTANT]
> In production, PALADIN depends on the service mesh (Linkerd mTLS) or an API gateway to authenticate and inject `X-Tenant-ID`. There is no JWT or OIDC validation inside the service itself.

## Authorization

- All tenant-scoped data reads/writes are filtered by `tenant_id`.
- PostgreSQL Row-Level Security (RLS) provides an additional enforcement layer when `security.enable_rls: true`:
  - Session variable `app.tenant_id` is set on every transaction.
  - RLS policies on all tables reject queries that don't match `current_setting('app.tenant_id', true)`.

## HTTP Middleware Stack (Ordered)

**Source:** `internal/middleware/http_stack.go`

| Order | Middleware | Description |
|---|---|---|
| 1 | `gin.Recovery()` | Recovers panics, returns `500 Internal Server Error` |
| 2 | `SecurityHeadersMiddleware()` | Adds HTTP security headers to all responses |
| 3 | `RequestSizeLimitMiddleware(10MB)` | Hard body size limit: `413 Request Entity Too Large` if exceeded |
| 4 | `cors.New(...)` | CORS headers; originates from `cors_allowed_origins` config |
| 5 | `RequestID(log, trustTenant)` | Extracts/generates `X-Request-Id`; extracts `X-Tenant-ID` if trusted |
| 6 | `ContextLogger(log)` | Attaches request-scoped logger to context |
| 7 | `RequestLogger(log)` | Structured access log after each request |
| 8 | `AuditLogMiddleware(auditRepo)` | Captures full request/response metadata to `audit_logs` table |
| 9 | `EnforceTenant(cfg)` | Rejects requests missing tenant context; admin key bypass |
| 10 | `OTelHTTP()` | OpenTelemetry trace instrumentation (only if `otel.enabled`) |

An additional `RateLimitMiddleware(cfg)` is applied globally after `SetupHTTPStack`.

## Security Headers

**Source:** `internal/middleware/security_headers.go`

The `SecurityHeadersMiddleware` adds the following headers to every response:

> [!NOTE]
> The exact headers added can be verified in `internal/middleware/security_headers.go`. Common secure headers include `X-Content-Type-Options`, `X-Frame-Options`, `X-XSS-Protection`, and `Referrer-Policy`.

## Input Validation

| Validation | Where | Details |
|---|---|---|
| Request body size | `RequestSizeLimitMiddleware` | Hard limit: 10 MiB (`max_body_bytes` default) |
| Request headers size | `max_header_bytes` | 1 MiB (Go stdlib `http.Server`) |
| Content-Type | `policy.allowed_content_types` | Enforced by `ObjectsService` before creating objects |
| Object size | `policy.max_object_size` | Enforced in service layer at object creation |
| Labels | `policy.labels_max_bytes`, `policy.labels_max_keys` | Enforced by service layer |
| External ref length | `policy.external_ref_max_len` | 256 chars max |
| Category slug | DB CHECK constraint | `^[a-z0-9][a-z0-9_-]{0,62}$` |
| Subpath | DB CHECK constraint | No `..`, no leading/trailing slashes, max 256 chars |
| Multipart part size | `policy.min_part_size`, `policy.max_part_size` | Validated in `InitiateMultipart` service |

## Secret Management

- Database passwords and S3 access/secret keys are never hardcoded.
- Resolved from Kubernetes Secrets at startup via `internal/config/resolver.go`.
- Values are injected into the config struct before any service initializes.

## CORS

- `cors_allowed_origins` defaults to `["*"]` (wildcard — local only).
- Restrict to specific origins in staging/production environments.
- Allowed methods: `GET, POST, PUT, DELETE, OPTIONS`
- Allowed headers: `Origin, Content-Type, Accept, Authorization, X-Request-ID, X-Tenant-ID, Idempotency-Key`
- Exposed headers: `Content-Length, X-Request-ID`
- Credentials allowed: `true`
- Preflight max age: `12h`

## TLS

- Both HTTP and gRPC servers support optional TLS (configured independently).
- HTTP TLS: `server.http.tls.enabled`, `cert_path`, `key_path`, `ca_path`.
- gRPC TLS: Unknown / not found in config — the gRPC TLS configuration is not present in `types.go` for `GRPCServer`.
- In Kubernetes, TLS termination is typically handled at the ingress/service-mesh layer.

## Tenant Mismatch Rejection

When `security.reject_tenant_mismatch: true`, requests where the route-param `tenant_id` differs from the header-derived tenant are rejected with `403 Forbidden`.

## Rate Limiting

- Per-tenant token bucket: 300 req/s sustained, burst 500.
- Exceeding limit: `429 Too Many Requests`.
- Tenant identified by `X-Tenant-ID` header (falls back to `"default"` if absent in rate limiter, which means unauthenticated requests share a bucket).
- Memory-bounded: max 10,000 active tenant limiters (LRU eviction).

## Circuit Breakers

- **Source:** `internal/breaker/`
- `gobreaker` library wraps S3 and/or DB operations.
- Circuit breaker state is exposed in the `/health/readyz` response under `breakers`.

## Recommendations (Not Yet Implemented)

| Recommendation | Rationale |
|---|---|
| Restrict CORS to explicit origins in staging/prod | Wildcard CORS allows any origin to make credentialed requests |
| Add gRPC TLS configuration | gRPC server currently has no TLS config struct |
| Implement JWT/OIDC validation | Currently reliant on Linkerd mTLS for tenant header authenticity |
| Add structured rate-limit headers with accurate remaining count | Current implementation returns static approximate values |
| Restrict `X-RateLimit-*` response headers to match configured values | `X-RateLimit-Limit: 100` is hardcoded; should reflect `rate_limit.requests_per_second` |
