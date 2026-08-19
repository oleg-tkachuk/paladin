# API — Paladin

## Interface

Paladin exposes a single Connect RPC API (Connect + Connect-Web + HTTP/JSON via transcoding).
All requests are validated against `buf.validate` proto annotations.

- **Proto definitions**: `proto/paladin/v1/`
- **Port**: `:8080` (HTTP/2 via h2c)
- **Handlers**: `internal/api/connect/`

---

## Services & RPCs

### ObjectService

Object lifecycle management.

| RPC | HTTP | Description |
|-----|------|-------------|
| `UploadObject` | `POST /v1/tenants/{tenant_id}/object_keys/{object_key}/objects` | Create object + presigned upload URL |
| `DownloadObject` | `GET /v1/tenants/{tenant_id}/object_keys/{object_key}/objects/{key}` | Metadata + presigned download URL |
| `GetObjectMetadata` | `GET /v1/tenants/{tenant_id}/object_keys/{object_key}/objects/{key}/metadata` | Metadata only (no download URL) |
| `UpdateObjectMetadata` | `PATCH /v1/tenants/{tenant_id}/object_keys/{object_key}/objects/{key}/metadata` | Partial metadata update via FieldMask |
| `DeleteObject` | `DELETE /v1/tenants/{tenant_id}/object_keys/{object_key}/objects/{key}` | Soft or hard delete (via `permanent` flag) |
| `CopyObject` | `POST .../objects/{key}:copy` | Server-side S3 copy |
| `MoveObject` | `POST .../objects/{key}:move` | Copy + soft-delete source |
| `ListObjects` | `GET /v1/tenants/{tenant_id}/object_keys/{object_key}/objects` | Paginated listing with rich filters |
| `CompleteObject` | `POST .../objects/{key}:complete` | Mark single-part upload as complete |
| `RestoreObject` | `POST .../objects/{key}:restore` | Recover a soft-deleted object |

### PresignService

Presigned URL generation for existing objects.

| RPC | HTTP | Description |
|-----|------|-------------|
| `GenerateUploadUrl` | `POST .../objects/{key}:presign-upload` | Presigned upload URL with optional TTL |
| `GenerateDownloadUrl` | `POST .../objects/{key}:presign-download` | Presigned download URL with optional TTL |

### MultipartUploadService

Multipart upload management.

| RPC | HTTP | Description |
|-----|------|-------------|
| `InitiateMultipartUpload` | `POST /v1/tenants/{tenant_id}/object_keys/{object_key}/uploads` | Start multipart session |
| `GeneratePartUploadUrl` | `POST .../uploads/{upload_id}/parts/{part_number}:sign` | Sign individual part |
| `CompleteMultipartUpload` | `POST .../uploads/{upload_id}:complete` | Finalize multipart |
| `AbortMultipartUpload` | `POST .../uploads/{upload_id}:abort` | Cancel multipart |
| `ListParts` | `GET .../uploads/{upload_id}/parts` | List uploaded parts |

### BulkService

Batch operations.

| RPC | HTTP | Description |
|-----|------|-------------|
| `BatchDeleteObjects`| `POST .../objects:batchDelete` | Delete up to 1000 objects |
| `BatchCopyObjects`  | `POST .../objects:batchCopy`   | Copy up to 1000 objects |

### SystemService

Health and diagnostics.

| RPC | HTTP | Description |
|-----|------|-------------|
| `Healthz` | `GET /paladin.v1.SystemService/Healthz` | Liveness check |
| `Readyz` | `GET /paladin.v1.SystemService/Readyz`   | Readiness check (Postgres + S3) |
| `Version` | `GET /paladin.v1.SystemService/Version`  | Build info |

### ObjectKeyService (Stub)

ObjectKey management — not yet implemented. See `docs/TODO.md`.

---

## Filtering (ListObjects)

The `ObjectFilter` message supports:

| Field | Type | Description |
|-------|------|-------------|
| `prefix` | `string` | Key prefix match |
| `tags` | `map<string,string>` | All tags must match |
| `min_size_bytes` | `int64` | Minimum size filter |
| `max_size_bytes` | `int64` | Maximum size filter |
| `modified_after` | `Timestamp` | Modified after timestamp |
| `modified_before` | `Timestamp` | Modified before timestamp |
| `status` | `ObjectStatus` | Status filter |
| `key_pattern` | `string` | Wildcard key matching |
| `content_type` | `string` | Exact content type match |

---

## Authentication & Authorization

- **Tenant Isolation**: Every request must include `tenant_id` as a proto field (validated as UUID).
- **Admin Key**: `Authorization: Bearer <admin-key>` for system-level access.
- **Tenant hint**: `X-Tenant-Id` may be set on `AuthService.Login` to disambiguate when a subject is registered in multiple tenants. The header is a hint only — password verification still gates the request, and the JWT's `tenant` claim is what every other RPC trusts.
- **RBAC**: Handled at the service layer via `domain.Policy` interface.

## Validation

All proto messages are validated at the interceptor level using `buf.build/go/protovalidate`.
Annotations include: `string.uuid`, `string.min_len`, `int64.gt`, `repeated.min_items`, `repeated.max_items`, `required`.
