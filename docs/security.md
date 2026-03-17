# Security - Paladin (PALADIN)

## Overview

PALADIN is designed with a "Secure by Default" mindset, focusing on tenant isolation and least-privilege access.

## 1. Authentication

Access is controlled via two primary mechanisms:

- **Tenant Context**: All requests must be associated with a valid `TenantId`. In production, this is usually extracted from a JWT or set by an upstream reverse proxy.
- **Admin Authentication**: Administrative endpoints (e.g., `/admin/config`) are protected by a shared secret (`auth.admin_key`).

## 2. Authorization & Isolation

- **Tenant Scoping**: All database queries and storage operations are strictly scoped by `tenant_id`.
- **Reject Tenant Mismatch**: If enabled (`security.reject_tenant_mismatch`), PALADIN will reject any request where the derived tenant ID doesn't match the one explicitly provided in the request body or path.
- **RLS (Planned)**: Future support for PostgreSQL Row Level Security to provide an additional layer of isolation at the database level.

## 3. Storage Security

- **Signed URLs**: Clients never get direct access to storage credentials. PALADIN issues time-limited pre-signed URLs (HMAC) for specific objects.
- **SSE (Server Side Encryption)**: PALADIN supports AES-256 or KMS-based encryption for objects at rest in S3/SeaweedFS.

## 4. Input Validation

- **JSON Schema**: All REST request bodies are validated against the OpenAPI specification.
- **Content Type Enforcement**: PALADIN rejects uploads with content types not in the `allowed_content_types` whitelist.
- **Size Limits**: Enforced at the control plane layer (`max_object_size`) and propagated to S3 via pre-signed URL conditions.

## 5. Secret Management

PALADIN integrates with the Kubernetes Secret API to resolve credentials for:

- Postgres Password
- S3 Access/Secret Keys
- Admin Tokens
- SSE KMS Keys
