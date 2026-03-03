# API Specifications

The `paladin` exposes both REST (HTTP) and gRPC interfaces for managing object storage and metadata.

## 1. REST API (OpenAPI 3.0.3)

Route prefixes are versioned (e.g., `/v1`).
All endpoints generally require `Bearer` token authentication unless explicitly marked otherwise. Multi-tenancy is enforced via the `X-Tenant-ID` header (or if trust is enabled, mapped internally).

### Standard Headers

- **X-Request-ID**: Correlation ID (UUID)
- **Traceparent**: W3C Trace context
- **Idempotency-Key**: Used for safe retries of state-mutating operations.

### Objects Lifecycle

- **`GET /objects`**
  - **Purpose**: List objects with filtering (category, status, prefix, external_ref) and pagination.
- **`POST /objects`**
  - **Purpose**: Create a single object and return a signed `PUT` URL for upload.
  - **Idempotency**: Supported.
- **`GET /objects/{id}`**
  - **Purpose**: Get object details (without signing download).
- **`HEAD /objects/{id}`**
  - **Purpose**: Check object existence headers (size, status, content-type).
- **`PATCH /objects/{id}`**
  - **Purpose**: Update object lifecycle status (abort, error). Soft-delete not allowed here.
  - **Idempotency**: Supported.
- **`DELETE /objects/{id}`**
  - **Purpose**: Soft delete an object.
  - **Idempotency**: Supported.
- **`POST /objects/{id}/restore`**
  - **Purpose**: Restore a soft-deleted object.
  - **Idempotency**: Supported.
- **`DELETE /objects/{id}/purge`**
  - **Purpose**: Permanently purge an object (hard delete).
  - **Idempotency**: Supported.

### Objects Bulk Operations

- **`POST /objects/bulk/delete`**
  - **Purpose**: Bulk soft delete objects.
- **`POST /objects/bulk/restore`**
  - **Purpose**: Bulk restore soft-deleted objects.
- **`DELETE /objects/bulk/purge`**
  - **Purpose**: Bulk permanently purge objects.

### Object Metadata & Pre-signing

- **`GET /objects/{id}/meta`**
  - **Purpose**: Get object metadata only (labels, external references).
- **`PATCH /objects/{id}/meta`**
  - **Purpose**: Update object metadata.
- **`POST /objects/{id}/sign-upload`**
  - **Purpose**: Re-issue signed upload action for an incomplete single upload.
- **`POST /objects/{id}/sign-download`**
  - **Purpose**: Issue signed download action (`GET`).
- **`POST /objects/{id}/complete`**
  - **Purpose**: Commit a single object upload. The server validates existence in S3 via `HEAD`.

### Multipart Uploads (Large Files)

- **`POST /multipart`**
  - **Purpose**: Initiate a multipart upload. Returns `UploadID`.
  - **Idempotency**: Supported.
- **`GET /multipart/{upload_id}`**
  - **Purpose**: Get multipart upload details.
- **`POST /multipart/{upload_id}/parts/{part_number}/sign`**
  - **Purpose**: Sign a single part for upload.
- **`POST /multipart/{upload_id}/parts/sign`**
  - **Purpose**: Sign multiple parts in batch.
- **`POST /multipart/{upload_id}/complete`**
  - **Purpose**: Complete and assemble a multipart upload.
- **`POST /multipart/{upload_id}/abort`**
  - **Purpose**: Abort a multipart upload.

### Categories

- **`GET /categories`**: List categories.
- **`POST /categories`**: Create a category.
- **`DELETE /categories/{slug}`**: Delete an empty category.
- **`GET /categories/{slug}/stats`**: Get category use statistics.

### Ops & Admin

- **`GET /ops/stats`**: Get overarching object statistics.
- **`GET /ops/s3/ping`**: Perform S3 `HEAD Bucket` check.
- **`GET /admin/audit-logs`**: List audit logs.
- **`GET /admin/audit-logs/{id}`**: Get audit log by ID.
- **`GET /admin/config`**: Get runtime configuration (redacted).
- **`GET /tenants`**: List active tenants.
- **Health Checks (`/health/livez`, `/health/readyz`, `/health/startupz`)**: Kubernetes probes.
- **`GET /version`**: Build constraints and versions.

---

## 2. gRPC API (`paladin.v1.Paladin`)

Defined in `proto/paladin.proto`. Provides equivalent endpoints for internal microservice communication without REST overhead.

- `CreateObject`
- `GetObject`
- `GetObjectMeta`
- `CompleteObject`
- `DeleteObject`
- `InitiateMultipart`
- `SignPart`
- `CompleteMultipart`
- `AbortMultipart`

### Internal Implementation

- Rest API endpoints use standard OpenAPI validation middleware.
- Handlers exist in `internal/api/http`.
- gRPC services exist in `internal/api/grpc`.
- Rate limiting is applied selectively based on tenant contexts.
