# API Reference

The service exposes two API surfaces:

1. **HTTP REST API** — OpenAPI-generated handlers via Gin (port `8080`)
2. **gRPC API** — Proto-defined `Paladin` service (port `9090`)

**Source:** `proto/paladin.proto`, `internal/api/http/adapter.go`, `internal/api/http/router.go`, `internal/generated/api/`

---

## Authentication

> **Source:** `internal/middleware/auth.go`, `internal/middleware/grpc_chain.go`

When `auth.enabled: true`:

- Requests must present `Bearer <admin_key>` in the `Authorization` header **OR** a valid tenant identity via `X-Tenant-ID`.
- Health endpoints (`/health/*`, `/version`) bypass authentication.

When `auth.enabled: false` (local/dev):

- All requests proceed without authentication.
- If `X-Tenant-ID` is absent, the tenant is set to `"default-tenant"`.

### Tenant Identity

| Header | Condition | Behavior |
|---|---|---|
| `X-Tenant-ID` | `security.trust_tenant_id_from_request: true` | Accepted as the tenant context |
| `Authorization: Bearer <admin_key>` | `auth.admin_key` is configured | Admin bypass; tenant set to `"system-admin"` if not provided |

Tenant context is enforced by the `EnforceTenant` middleware (HTTP) and `AuthInterceptor` + `EnforceTenantInterceptor` chain (gRPC).

---

## HTTP REST API

### Base URL

```
http://<host>:8080
```

### Common Request Headers

| Header | Required | Description |
|---|---|---|
| `X-Tenant-ID` | Conditional | Tenant identity (required when `auth.enabled: true`) |
| `Authorization` | Optional | `Bearer <admin_key>` for admin bypass |
| `X-Request-Id` | Optional | Client-provided request correlation ID |
| `Idempotency-Key` | Optional | Client-provided idempotency key for mutating operations |
| `Content-Type` | Required for POST/PATCH | `application/json` |

### Common Response Headers

| Header | Description |
|---|---|
| `X-Request-Id` | Echoed or generated request ID |
| `X-Api-Version` | Service version string |
| `X-RateLimit-Limit` | Configured rate limit |
| `X-RateLimit-Remaining` | Remaining requests in current window |

### Error Model

All errors return a JSON body:

```json
{
  "error": "<error_code>",
  "message": "<human-readable message>"
}
```

Rate limit errors return HTTP `429` with:

```json
{ "error": "rate_limit_exceeded" }
```

---

### Infrastructure Endpoints

#### `GET /health/livez`

Liveness probe. Returns `200 OK` always after process start.

```json
{ "status": "alive" }
```

#### `GET /health/readyz`

Readiness probe. Checks PostgreSQL and S3 connectivity.

- `200 OK` — `{ "status": "ready", "dependencies": {...} }`
- `503 Service Unavailable` — `{ "status": "not_ready", "reason": "dependency_unavailable", "dependencies": {...} }`

#### `GET /health/startupz`

Startup probe. Returns `503` until the app has completed initialization.

- `200 OK` — `{ "status": "started" }`
- `503 Service Unavailable` — `{ "status": "starting", "reason": "initialization_in_progress" }`

#### `GET /metrics`

Prometheus metrics scrape endpoint. No authentication.

#### `GET /version` or `GET /v1/version`

Returns service version info.

```json
{
  "version": "1.2.3",
  "commit": "abc123",
  "build_time": "2026-01-01T00:00:00Z"
}
```

---

### Object Endpoints

All object endpoints are under the `/v1/` prefix and require a tenant context.

#### `POST /v1/objects` — Create Object (Single Upload)

Initiates a single-part object upload. Returns a pre-signed PUT URL.

**Request Body:**

```json
{
  "content_type": "image/jpeg",
  "size_bytes": 1048576,
  "labels": { "env": "prod" },
  "external_ref": "my-unique-ref",
  "category": "images",
  "upload_expires_in_seconds": 900
}
```

**Response `201 Created`:**

```json
{
  "object_id": "550e8400-...",
  "object_key": "tenant-1/images/550e8400-...",
  "bucket": "paladin",
  "status": "pending",
  "upload": {
    "url": "https://...",
    "method": "PUT",
    "headers": {},
    "expires_at": "2026-01-01T00:15:00Z"
  }
}
```

**Idempotency:** Supports `Idempotency-Key` header. Replays cached response on repeat.

**Side effects:** Inserts row into `objects` table (status: `pending`). Generates pre-signed PUT URL via S3.

---

#### `GET /v1/objects` — List Objects

Cursor-based paginated list of tenant objects.

**Query Parameters:**

| Param | Type | Description |
|---|---|---|
| `limit` | int | Max items per page (default 100) |
| `cursor` | string | Pagination cursor from previous response |
| `status` | string | Filter by object status |
| `category` | string | Filter by category slug |
| `external_ref` | string | Filter by external reference |
| `prefix` | string | Filter by object key prefix |
| `created_after` | datetime | Filter by creation time lower bound |
| `created_before` | datetime | Filter by creation time upper bound |
| `sort` | string | Sort field (`created_at`) |
| `order` | string | Sort order (`asc`, `desc`) |

**Response `200 OK`:**

```json
{
  "items": [{ "object_id": "...", "status": "complete", ... }],
  "pagination": {
    "next_cursor": "...",
    "has_more": true,
    "total_count": 1234
  }
}
```

---

#### `GET /v1/objects/:id` — Get Object

Returns full object record including pre-signed download URL.

**Response `200 OK`:** Full `ObjectCommon` with signed download URL.

---

#### `HEAD /v1/objects/:id` — Check Object Existence

Returns `200 OK` if object exists, `404 Not Found` otherwise. No body.

---

#### `GET /v1/objects/:id/meta` — Get Object Metadata

Returns object metadata without a download URL.

---

#### `PATCH /v1/objects/:id/meta` — Update Object Metadata

Update `labels` and `external_ref` fields.

**Request Body:**

```json
{
  "labels": { "tag": "new-value" },
  "external_ref": "new-ref"
}
```

---

#### `POST /v1/objects/:id/complete` — Complete Object Upload

Marks a single-upload object as complete after the client has PUT the file.

**Request Body (optional):**

```json
{ "etag": "abc123", "size_bytes": 1048576 }
```

**Response `200 OK`:**

```json
{
  "status": "complete",
  "stored_etag": "abc123",
  "stored_size_bytes": 1048576,
  "completed_at": "2026-01-01T00:00:00Z"
}
```

---

#### `PATCH /v1/objects/:id` — Update Object Status

Manually transition an object to a new status.

**Request Body:**

```json
{ "status": "complete" }
```

---

#### `POST /v1/objects/:id/sign-upload` — Re-sign Upload URL

Generate a new pre-signed PUT URL for an existing object.

---

#### `POST /v1/objects/:id/sign-download` — Sign Download URL

Generate a pre-signed GET URL for an existing object.

---

#### `DELETE /v1/objects/:id` — Soft Delete Object

Transitions object to `soft_deleted` status.

**Response:** `204 No Content`

---

#### `DELETE /v1/objects/:id/purge` — Purge Object (Hard Delete)

Permanently removes an object from both S3 and the database.

**Query/Header:** `Idempotency-Key` supported.

**Response:** `204 No Content`

---

#### `POST /v1/objects/:id/restore` — Restore Soft-Deleted Object

Transitions a `soft_deleted` object back to `complete`.

**Response `200 OK`:** Restored object record.

---

#### `GET /v1/objects/stats` — Object Statistics

Returns aggregate counts and total size per tenant.

**Response `200 OK`:**

```json
{
  "total_count": 5000,
  "total_size": 1073741824,
  "pending_count": 10,
  "uploading_count": 5,
  "uploaded_count": 20,
  "complete_count": 4960,
  "soft_deleted_count": 5
}
```

---

#### `POST /v1/objects/bulk-delete` — Bulk Soft Delete

Soft-delete multiple objects by ID.

**Request Body:**

```json
{ "ids": ["uuid-1", "uuid-2"] }
```

**Response `200 OK`:**

```json
{ "affected_count": 2 }
```

---

#### `POST /v1/objects/bulk-restore` — Bulk Restore

Restore multiple soft-deleted objects.

---

#### `POST /v1/objects/bulk-purge` — Bulk Purge (Hard Delete)

Permanently remove multiple objects. Supports `Idempotency-Key`.

---

### Multipart Upload Endpoints

#### `POST /v1/multipart` — Initiate Multipart Upload

Initiates an S3 multipart upload session.

**Request Body:**

```json
{
  "content_type": "application/octet-stream",
  "size_bytes": 10737418240,
  "labels": {},
  "external_ref": "large-file-ref",
  "category": "archives",
  "upload_expires_in_seconds": 86400
}
```

**Response `200 OK`:**

```json
{
  "object_id": "...",
  "object_key": "tenant-1/archives/...",
  "upload_id": "s3-upload-id",
  "part_size": 8388608,
  "expires_at": "2026-01-02T00:00:00Z",
  "bucket": "paladin",
  "status": "uploading"
}
```

**Idempotency:** Supports `Idempotency-Key` header.

---

#### `GET /v1/multipart/:upload_id` — Get Multipart Upload

Returns current state of a multipart upload session.

---

#### `GET /v1/multipart/:upload_id/parts/:part_number/sign` — Sign Part URL

Returns a pre-signed PUT URL for a single part.

**Response `200 OK`:**

```json
{
  "part_number": 1,
  "upload": { "url": "...", "method": "PUT", "headers": {}, "expires_at": "..." }
}
```

---

#### `POST /v1/multipart/:upload_id/parts/batch-sign` — Batch Sign Parts

Returns pre-signed PUT URLs for multiple part numbers in one call.

**Request Body:**

```json
{ "part_numbers": [1, 2, 3] }
```

---

#### `POST /v1/multipart/:upload_id/complete` — Complete Multipart Upload

Notifies S3 that all parts are uploaded and finalizes the object.

**Request Body:**

```json
{
  "parts": [
    { "part_number": 1, "etag": "abc" },
    { "part_number": 2, "etag": "def" }
  ]
}
```

**Response `200 OK`:**

```json
{
  "object_id": "...",
  "status": "complete",
  "stored_etag": "...",
  "stored_size_bytes": 10737418240,
  "completed_at": "..."
}
```

---

#### `DELETE /v1/multipart/:upload_id` — Abort Multipart Upload

Aborts the multipart upload session and instructs S3 to clean up parts.

**Response `200 OK`:**

```json
{ "status": "aborted" }
```

---

### Category Endpoints

#### `GET /v1/categories` — List Categories

Cursor-paginated list of categories for the tenant.

**Query Params:** `limit`, `cursor`

**Response `200 OK`:**

```json
{
  "items": [{ "id": "...", "slug": "images", "name": "Images", "description": null }],
  "pagination": { "next_cursor": null, "has_more": false, "total_count": 5 }
}
```

---

#### `POST /v1/categories` — Create Category

**Request Body:**

```json
{ "slug": "images", "name": "Images", "description": "Optional description" }
```

**Response `201 Created`:** Category record.

---

#### `GET /v1/categories/:slug` — Get Category

**Response `200 OK`:** Category record.

---

#### `DELETE /v1/categories/:slug` — Delete Category

**Response `204 No Content`**

---

#### `GET /v1/categories/:slug/stats` — Category Stats

**Response `200 OK`:**

```json
{ "total_count": 100, "total_size": 1073741824 }
```

---

### Admin Endpoints

#### `GET /v1/admin/config` — Get Redacted Config

Returns non-sensitive configuration fields (passwords and secrets are redacted).

---

## gRPC API

**Proto package:** `paladin.v1`
**Service:** `Paladin`
**Source:** `proto/paladin.proto`

All RPCs accept a `tenant_id` field in the request message. Interceptor chain: Recovery → RequestID → ContextLogger → Logger → Auth → EnforceTenant.

### Methods

| RPC | Request | Response | Description |
|---|---|---|---|
| `CreateObject` | `CreateObjectRequest` | `CreateObjectResponse` | Create single-upload object |
| `GetObject` | `GetObjectRequest` | `GetObjectResponse` | Get object with download URL |
| `GetObjectMeta` | `GetObjectRequest` | `GetObjectMetaResponse` | Get object metadata only |
| `CompleteObject` | `CompleteObjectRequest` | `CompleteObjectResponse` | Mark object upload complete |
| `DeleteObject` | `DeleteObjectRequest` | `DeleteObjectResponse` | Soft-delete object |
| `InitiateMultipart` | `InitiateMultipartRequest` | `InitiateMultipartResponse` | Start multipart upload |
| `SignPart` | `SignPartRequest` | `SignPartResponse` | Sign individual part URL |
| `CompleteMultipart` | `CompleteMultipartRequest` | `CompleteMultipartResponse` | Complete multipart upload |
| `AbortMultipart` | `AbortMultipartRequest` | `AbortMultipartResponse` | Abort multipart upload |
| `ListCategories` | `ListCategoriesRequest` | `ListCategoriesResponse` | List categories (paginated) |
| `GetCategory` | `GetCategoryRequest` | `GetCategoryResponse` | Get single category |
| `CreateCategory` | `CreateCategoryRequest` | `GetCategoryResponse` | Create category |
| `DeleteCategory` | `DeleteCategoryRequest` | `DeleteCategoryResponse` | Delete category |
| `GetCategoryStats` | `GetCategoryStatsRequest` | `GetCategoryStatsResponse` | Category object stats |
| `GetObjectStats` | `GetObjectStatsRequest` | `GetObjectStatsResponse` | Tenant-level object stats |
| `ListObjects` | `ListObjectsRequest` | `ListObjectsResponse` | List objects (paginated) |

### Example (grpcurl)

```bash
# List categories
grpcurl -plaintext \
  -H 'x-tenant-id: my-tenant' \
  -d '{"tenant_id": "my-tenant", "limit": 10}' \
  localhost:9090 \
  paladin.v1.Paladin/ListCategories

# Create object
grpcurl -plaintext \
  -H 'x-tenant-id: my-tenant' \
  -d '{"tenant_id":"my-tenant","content_type":"image/jpeg","size_bytes":4096,"category":"images"}' \
  localhost:9090 \
  paladin.v1.Paladin/CreateObject
```

---

## Rate Limiting

Implemented per-tenant using a token bucket (`golang.org/x/time/rate`).

- Default: 300 requests/second, burst of 500
- Applied to all HTTP API routes (`/v1/...`)
- Exceeding the limit: `HTTP 429 Too Many Requests`
- Headers: `X-RateLimit-Limit: 100`, `X-RateLimit-Remaining: N`

> [!NOTE]
> The `X-RateLimit-Limit` and `X-RateLimit-Remaining` response headers currently return approximate/static values (`100`, `99`). Actual enforcement uses the configured `requests_per_second` / `burst` values. This is a known limitation noted in the code with `TODO` comments.

## Idempotency

- Enabled via `idempotency.enabled: true`
- Client sends `Idempotency-Key` header on mutating requests
- First request is executed and result stored in the `idempotency_keys` table
- Repeat requests with the same key (within TTL) return the cached response
- Key is scoped by `(tenant_id, idempotency_key)` composite primary key
