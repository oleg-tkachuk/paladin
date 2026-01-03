# paladin

Presign-only control plane for S3-compatible object storage (AWS S3 / SeaweedFS / MinIO-compatible).

## Features

- **Metadata Management**: Atomically tracks object metadata and multipart upload states in PostgreSQL.
- **Secure Access**: Generates time-limited presigned URLs for single-part and multipart uploads/downloads.
- **Lifecycle Management**: Soft-delete objects with status tracking (pending, active, deleted).
- **High Performance**: Built with Gin (HTTP) and gRPC for low-latency control plane operations.
- **Schema-first Config**: Uses CUE for strict configuration validation and smart defaulting.
- **Advanced Logging**: Structured JSON/Console logging with support for all Zap levels and dynamic sampling.

## Tech Stack

- **Server**: [Gin](https://gin-gonic.com/) (HTTP), [gRPC](https://grpc.io/)
- **Database**: [PostgreSQL](https://www.postgresql.org/) with [pgx](https://github.com/jackc/pgx)
- **Config**: [CUE](https://cuelang.org/)
- **Logging**: [Zap](https://github.com/uber-go/zap)
- **Observability**: [OpenTelemetry](https://opentelemetry.io/)

## Project Structure

```text
├── cmd/server          # Application entrypoint (Cobra CLI)
├── configs/            # Configuration files
├── deploy/             # Docker and Kubernetes deployment manifests
├── internal/
│   ├── api/            # HTTP and gRPC transport layers
│   ├── app/            # Application wire-up logic
│   ├── config/         # CUE-powered configuration parsing
│   ├── logger/         # Structured logger initialization
│   ├── service/        # Core business logic (presigning, state management)
│   ├── storage/        # S3 client and storage abstractions
│   └── store/          # PostgreSQL repository implementations
└── migrations/         # SQL migration files
```

## Configuration

The service uses a CUE schema (`internal/config/schema.cue`) for validation. Configuration is loaded from `configs/paladin.yaml`.

### Logger Configuration

```yaml
logger:
  level: info           # Options: debug, info, warn, error, dpanic, panic, fatal
  format: json          # Options: json, console
  development: false    # Enables development-friendly panic behavior
  disable_caller: false  # Disables file/line number reporting
  disable_stacktrace: false # Disables stacktraces for error logs
```

### Server Configuration

```yaml
server:
  mode: release         # release, debug, test
  log_probes: false     # Toggles logging for health check probes
```

### Health Probes

Exposes standard endpoints following Kubernetes best practices:

- `GET /health/livez` - Liveness probe (Restart logic). Checks if process is responsive.
- `GET /health/startupz` - Startup probe (Initialization). Checks if config and clients are ready.
- `GET /health/readyz` - Readiness probe (Traffic). Checks connectivity to Postgres and SeaweedFS (S3).

## Testing

The project uses [Ginkgo](https://onsi.github.io/ginkgo/) and [Gomega](https://onsi.github.io/gomega/) for BDD-style unit testing, and [Hurl](https://hurl.dev/) for integration testing.

```bash
# Run all unit tests
task test

# Run Hurl integration tests
task test:hurl

# Or using go test directly
go test -v ./internal/service/...
```

## Local Development

```bash
# Build the server binary and Docker image
task build

# Start the full stack (Postgres + SeaweedFS + PALADIN)
task up

# Run unit tests
task test

# Verify the service is ready
curl -s http://localhost:8080/health/readyz | jq .
```

## API Endpoints

### HTTP API

**Health & Monitoring:**

- `GET /health/livez` - Liveness probe
- `GET /health/startupz` - Startup probe
- `GET /health/readyz` - Readiness probe
- `GET /version` - Version information
- `GET /metrics` - Prometheus metrics

**Object Management (v1):**

- `POST /v1/objects` - Create single object upload (returns presigned PUT URL)
- `GET /v1/objects/:id` - Get object metadata (returns presigned GET URL)
- `POST /v1/objects/:id/complete` - Mark object as active after upload
- `DELETE /v1/objects/:id` - Soft-delete object (marks as deleted)

**Multipart Uploads (v1):**

- `POST /v1/multipart` - Initiate multipart upload
- `POST /v1/multipart/:upload_id/parts/:part_number/sign` - Sign individual part
- `POST /v1/multipart/:upload_id/complete` - Complete multipart upload
- `POST /v1/multipart/:upload_id/abort` - Abort multipart upload

### gRPC API

- `Paladin/PresignPut`
- `Paladin/PresignGet`
- `Paladin/CreateMultipartUpload`
- `Paladin/PresignUploadPart`
- `Paladin/CompleteMultipartUpload`
- `Paladin/AbortMultipartUpload`

## Architecture

The service follows an interface-first design to ensure testability and maintainability:

- **Service Layer**: Decoupled from storage and database using Go interfaces.
- **Circuit Breakers**: Distributed via a central factory for consistent fault tolerance.
- **State Management**: Atomic state transitions for multipart uploads.

## Notes

- **Data Flow**: This service intentionally does not proxy or stream object data. It only manages metadata and signs access URLs.
- **S3 Connectivity**: The `readyz` probe performs a `HeadBucket` operation to verify S3 connectivity.
