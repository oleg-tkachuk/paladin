# API - Paladin (PALADIN)

## Interfaces

PALADIN exposes a dual API interface to accommodate both internal service-to-service communication and external client interactions.

### 1. HTTP/REST API (OpenAPI)

- **Spec**: [api/openapi.yaml](file:///workspace/api/openapi.yaml)
- **Port**: `:8080` (default)
- **Implementation**: [internal/api/http/adapter.go](file:///workspace/internal/api/http/adapter.go)

### 2. gRPC API (Connect)

- **Spec**: [proto/paladin.proto](file:///workspace/proto/paladin.proto)
- **Port**: `:8081` (default)
- **Implementation**: [internal/api/grpc/server.go](file:///workspace/internal/api/grpc/server.go)

---

## Core Endpoints (REST)

### Objects Management

- `POST /v1/objects`: Create object metadata and initiate upload.
- `GET /v1/objects/{id}`: Retrieve object metadata.
- `DELETE /v1/objects/{id}`: Soft delete an object (move to `deleted` status).
- `POST /v1/objects/{id}/purge`: Hard delete an object (remove from storage).
- `POST /v1/objects/{id}/restore`: Restore a soft-deleted object.

### Multipart Uploads

- `POST /v1/multipart/initiate`: Start a multi-part upload session.
- `POST /v1/multipart/{upload_id}/sign-batch`: Batch sign upload URLs for parts.
- `POST /v1/multipart/{upload_id}/complete`: Finalize multi-part upload.

### Presigned Actions

- `POST /v1/objects/{id}/sign-upload`: Refresh a pre-signed upload URL.
- `POST /v1/objects/{id}/sign-download`: Generate a pre-signed download URL.

### System & Health

- `GET /health/livez`: Liveness probe.
- `GET /health/readyz`: Readiness probe (checks Postgres and S3 connectivity).
- `GET /admin/config`: Retrieve current system configuration (admin only).

---

## Authentication & Authorization

- **Tenant Isolation**: Every request must include a `X-Tenant-Id` header (or established via JWT/Proxy).
- **Admin Key**: Direct access to admin endpoints requires `Authorization: Bearer <admin-key>`.
- **RBAC**: Handled at the service layer via tenant scoping.
