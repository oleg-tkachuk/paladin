# Security

The `paladin` is designed as an internal microservice, heavily prioritizing structural multi-tenancy and credential safety.

## 1. Authentication & Authorization

### Implemented

- **Trusted Tenancy:** The service relies on an upstream API Gateway (or service mesh) to authenticate users and assert the tenant context via the `X-Tenant-ID` HTTP header.
- **Enforcement Middleware:** `middleware.EnforceTenant` strictly rejects requests lacking a valid tenant context (excluding system probes like `/health/livez`).
- **Path-Tenant Validation:** If the route includes a `:tenant_id` path parameter, the middleware ensures it matches the `X-Tenant-ID` header. This strict matching prevents token-swapping attacks. (Config: `security.reject_tenant_mismatch`).

### Recommendations (Not Implemented)

- The service currently accepts `X-Tenant-ID` as a plaintext header. It is highly recommended to ensure the network boundary strips arbitrary client-provided `X-Tenant-ID` headers to prevent tenant spoofing, relying strictly on a trusted API Gateway.

## 2. Data Isolation (Row-Level Security)

- **Implemented:** The Postgres database employs strict **Row-Level Security (RLS)**.
- Every transaction block initiates with `SET LOCAL app.tenant_id = '<tenant>'`.
- DB queries cannot accidentally leak rows belonging to a different tenant because the Postgres kernel enforces the `tenant_id` WHERE clause universally on tables like `objects`, `multipart_uploads`, and `audit_logs`.

## 3. Secret Management

- **Implemented:** The application seamlessly supports Kubernetes Secrets for critical credentials.
- Config properties like `password_secret` (for Postgres) and `access_key_secret` / `secret_key_secret` (for S3) can reference K8s secrets.
- An internal HTTP resolver (`internal/config/resolver.go`) queries a sidecar or secret-reader service to securely fetch these secrets at bootstrap, preventing them from being stored in plaintext YAML files or environment variables.

## 4. Input Validation & Limits

- **Implemented:**
  - **OpenAPI Validation:** Requests are strictly validated against `openapi.yaml` via code-generated middleware (`middleware.OapiRequestValidator`).
  - **Payload Limits:** Business logic validates file sizes against `policy.max_object_size` and multipart bounds (`min_part_size`, `max_part_size`).
  - **Content Types:** Allowed MIME types are checked against an explicitly permitted list in configuration.
  - **Rate Limiting:** Scaffolding exists for tenant-based rate limiting (headers `X-RateLimit-*`), with the ability to cap requests per second (`rate_limit.requests_per_second`).

## 5. Audit Logging

- **Implemented:** All mutable and critical access requests are captured by `middleware.AuditLogger` and stored in the `audit_logs` table. Fields like `actor_type`, `client_ip`, `path`, and HTTP outcomes are immutably recorded for compliance.

## 6. TLS / mTLS

- **Implemented:** The internal HTTP server can be configured to serve traffic over TLS (`server.http.tls.enabled`, `cert_path`, `key_path`). mTLS is assumed to be offloaded to a service mesh (like Linkerd or Istio).

## 7. Protected Data Handling

- **Implemented:** Passwords and credentials are NEVER logged. The main request logger prevents potentially sensitive URL bodies or headers from entering stdout. However, if `security.log_sensitive` is explicitly enabled in dev environments, deeper request introspection is permitted.
