# Architecture

## Overview

The `paladin` is a Go-based service providing object storage management and control. It acts as an abstraction over S3-compatible storage, maintaining its own metadata and policies in a PostgreSQL database.

## 1. Inventory Map

- **Language:** Go 1.25
- **Frameworks:**
  - HTTP Server: `github.com/gin-gonic/gin`
  - gRPC Server: `google.golang.org/grpc`
  - Dependency Injection: `github.com/google/wire`
  - SQL generation: `github.com/sqlc-dev/sqlc`
  - OpenAPI Code Generation: `github.com/oapi-codegen/oapi-codegen/v2`
- **Runtime Entrypoints:** `cmd/server/main.go`
- **Build System:** `Taskfile.yaml`, `go mod`
- **Configuration Management:** Custom YAML unmarshaler backing `internal/config.Config`

## 2. Service Boundaries

### Internal Domain

- **API Layer:** Exposes HTTP (REST) and gRPC endpoints for clients to interact with object metadata and storage operations.
- **Service Layer:** Business logic for object lifecycle management (upload, part uploads, presiging, deletion, policies).
- **Storage Layer:** Abstractions over the PostgreSQL database (using `sqlc`) and the S3-compatible backend (using `aws-sdk-go-v2`).
- **Worker Layer:** Background jobs (e.g., `Reaper`) for data lifecycle management like cleaning up orphaned or pending objects.

### External Dependencies

1. **PostgreSQL:**
   - Stores metadata about objects, multipart uploads, labels, policies, and audit logs.
   - Accessed via `pgx/v5` and custom connection pooling.
2. **S3-Compatible Storage:**
   - Actually stores the object blobs.
   - Interactions managed via AWS SDK `s3`.
3. **OpenTelemetry Collector (Optional):**
   - For traces and metrics export via OTLP.

## 3. Runtime Entry Points

The primary entry point is located at `cmd/server/main.go`, which invokes the CLI execution defined in `cmd/server/root.go`.

**Bootstrap Logic (`internal/app/app.go`):**

1. **Configuration Loading:** Reads configuration from the YAML file specified by the `-c` flag (`/app/configs/paladin.yaml` by default).
2. **Logger Initialization:** Sets up `zap` logger according to config.
3. **Telemetry:** Initializes OpenTelemetry providers if enabled.
4. **Database Connection:** Connects to PostgreSQL and initializes the `pgxpool`.
5. **Storage Client:** Sets up the AWS S3 client.
6. **Servers:**
   - Initializes and starts the gRPC server.
   - Initializes and starts the HTTP (Gin) server (optionally TLS-enabled).
7. **Workers:** Starts background processes, like the Reaper.
8. **Graceful Shutdown:** Listens for `SIGINT`/`SIGTERM` to coordinate graceful termination of servers and dependencies.

## 4. Directory Structure Snapshot

- `/api`: Contains the OpenAPI 3.0 specification (`openapi.yaml`).
- `/cmd/server`: Application entry points (`main.go`, `root.go`, wire injection).
- `/configs`: Configuration templates and environment-specific configs.
- `/docs`: Markdown documentation (where this file resides).
- `/internal`: Private application code.
  - `/internal/config`: Configuration schema and parsing.
  - `/internal/app`: Core application container and bootstrap.
  - `/internal/domain`: Core domain models.
  - `/internal/service`: Application business logic.
  - `/internal/store`: Data access layers (Postgres / S3).
  - `/internal/worker`: Background jobs.
- `/migrations`: Goose-compatible PostgreSQL migration files.
- `/proto`: Protobuf definitions and generated code.
