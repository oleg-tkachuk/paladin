# Architecture Snapshot - Paladin (PALADIN)

## Overview

The Paladin (PALADIN) is a multi-tenant service responsible for managing the lifecycle of binary objects (files, documents, images). It provides a unified API for object storage, abstraction over physical storage (S3/SeaweedFS), and tenant isolation enforced by Cedar policies.

## Inventory Map

- **Language**: Go
- **Frameworks**:
  - [Connect RPC](https://connectrpc.com/): Connect-compatible RPC framework (replaces Gin).
  - [Protobuf + buf/validate](https://buf.build/bufbuild/protovalidate): Contract-first API with declarative validation.
  - [SQLC](https://sqlc.dev/): Type-safe SQL generator.
  - [Google Wire](https://github.com/google/wire): Dependency injection.
  - [CUE](https://cuelang.org/): Configuration schema validation.
  - [Koanf](https://github.com/knadh/koanf): Configuration management.

## Service Topology & Dependencies

### Upstream Dependencies

- **PostgreSQL**: Primary metadata store (objects, tenants, audit logs).
- **S3-compatible Storage (e.g., SeaweedFS)**: Physical object storage.
- **OpenTelemetry Collector**: For distributed tracing and metrics.

### Downstream Consumers

- **Tenant applications**: Consume PALADIN for document and asset management.
- **Workflow workers**: Use PALADIN for hard deletion and object lifecycle management.
- **Frontend applications**: Directly consume signed URLs for uploads and downloads.

### Data Flow

1. **Metadata Registration**: Metadata for an object is stored in Postgres.
2. **Signed Action**: PALADIN provides pre-signed S3 URLs for direct client-to-storage upload/download.
3. **Completion**: Clients notify PALADIN when an upload is complete to finalize metadata.
4. **Lifecycle**: Background workers (Reaper) handle cleanup of failed or expired uploads.

## Transport Layer

All RPCs are served via Connect RPC on a single HTTP/2 port (`:8080`). The transport layer is organized into per-service handlers:

| Handler | File | Service |
|---------|------|---------|
| `ObjectHandler` | `internal/api/connect/object_handler.go` | Object lifecycle (upload, download, copy, move, delete, list) |
| `MultipartHandler` | `internal/api/connect/multipart_handler.go` | Multipart upload (initiate, sign parts, complete, abort, list parts) |
| `PresignHandler` | `internal/api/connect/presign_handler.go` | Presigned URL generation (upload, download) |
| `BulkHandler` | `internal/api/connect/bulk_handler.go` | Batch operations (batch delete, batch copy) |
| `ObjectKeyHandler` | `internal/api/connect/bucket_handler.go` | ObjectKey management (stub — see `docs/TODO.md`) |
| `SystemHandler` | `internal/api/connect/system_handler.go` | Health, readiness, version, config |

### Shared Components

| File | Purpose |
|------|---------|
| `internal/api/connect/errors.go` | Domain-error → Connect-code mapping |
| `internal/api/connect/mappers.go` | Domain ↔ proto type converters |

### Interceptor Chain (Connect)

Defined in `internal/middleware/connect_chain.go`:

1. **Recovery** — panic catch → `CodeInternal`
2. **RequestID** — extract/generate `x-request-id`
3. **ContextLogger** — enrich zap logger with request_id
4. **Auth** — extract tenant from header/admin key
5. **Logger** — access log (method, code, tenant, latency)
6. **EnforceTenant** — reject unauthenticated when auth enabled
7. **Validation** — `buf/validate` proto annotation enforcement via `protovalidate`
8. **RateLimit** — per-tenant token object_key

## Runtime Entry Points

- **HTTP Server**: `internal/api/http/router.go` — Connect RPC + operational endpoints.
- **Main**: `cmd/server/main.go` — Service bootstrap.
- **DI**: `internal/wire/sets.go` — Wire provider graph.

## Configuration Sources

- **YAML**: `configs/paladin.yaml`
- **Environment Variables**: Prefixed with `PALADIN_`.
- **K8s Secrets**: Automatically resolved if running in Kubernetes.

## Source Index

- `internal/api/connect/` — Per-service RPC handlers, mappers, errors.
- `internal/api/http/` — HTTP server, CORS, operational routes.
- `internal/service/` — Business logic and orchestrators.
- `internal/store/` — Database repositories (Postgres + cache).
- `internal/storage/s3/` — S3 client implementation.
- `internal/domain/` — Model definitions and interfaces.
- `internal/middleware/` — Interceptors (auth, logging, validation, rate limit).
- `internal/worker/` — Background jobs (Reaper).
- `proto/paladin/v1/` — Protobuf service definitions.
