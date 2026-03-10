# Architecture Snapshot - Paladin (PALADIN)

## Overview

The Paladin (PALADIN) is a central service in the acme ecosystem responsible for managing the lifecycle of binary objects (files, documents, images). It provides a unified API for object storage, abstraction over physical storage (S3/SeaweedFS), and robust multi-tenant isolation.

## Inventory Map

- **Language**: Go
- **Frameworks**:
  - [Gin](https://github.com/gin-gonic/gin): HTTP web framework.
  - [Connect](https://connectrpc.com/): gRPC-compatible RPC framework.
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

- **acme consumer API**: Consumes PALADIN for document management.
- **Workflows Workers**: Use PALADIN for hard deletion and object lifecycle management.
- **Frontend Applications**: Directly consume signed URLs for uploads and downloads.

### Data Flow

1. **Metadata Registration**: Metadata for an object is stored in Postgres.
2. **Signed Action**: PALADIN provides pre-signed S3 URLs for direct client-to-storage upload/download.
3. **Completion**: Clients notify PALADIN when an upload is complete to finalize metadata.
4. **Lifecycle**: Background workers (Reaper) handle cleanup of failed or expired uploads.

## Runtime Entry Points

- **HTTP Server**: `internal/api/http/router.go` - Entry point for REST/OpenAPI requests.
- **gRPC Server**: `internal/api/grpc/server.go` - Entry point for internal service-to-service RPCs.
- **Main**: `cmd/server/main.go` - Service bootstrap.

## Configuration Sources

- **YAML**: `configs/paladin.yaml`
- **Environment Variables**: Prefixed with `PALADIN_`.
- **K8s Secrets**: Automatically resolved if running in Kubernetes.

## Source Index

- [internal/api/](file:///workspace/internal/api/) - API Handlers (HTTP/gRPC).
- [internal/service/](file:///workspace/internal/service/) - Business logic and orchestrators.
- [internal/store/](file:///workspace/internal/store/) - Database repositories.
- [internal/domain/](file:///workspace/internal/domain/) - Model definitions and interfaces.
