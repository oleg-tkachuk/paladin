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

## Database Schema

The service uses PostgreSQL to track object metadata and multipart upload state. The schema is defined in [`migrations/001_init.sql`](migrations/001_init.sql).

### Tables

#### `objects`

Tracks the lifecycle of each object with the following states:

- **pending** - Object created, awaiting upload completion
- **active** - Upload completed and verified
- **deleted** - Soft-deleted (marked for cleanup)

Key fields:

- `id` (UUID) - Primary key
- `tenant_id` - Multi-tenancy isolation
- `object_key` - S3 object key (unique per tenant)
- `bucket` - S3 bucket name
- `content_type` - MIME type
- `size_bytes` - Object size
- `checksum_sha256` - Optional integrity checksum
- `status` - Lifecycle state
- `expires_at` - Optional expiration timestamp

Indexes:

- `idx_objects_tenant_created_at` - Query objects by tenant and creation time
- `uq_objects_tenant_key` - Enforce unique object keys per tenant

#### `multipart_uploads`

Manages multipart upload sessions with states:

- **initiated** - Upload session started
- **completed** - All parts uploaded and finalized
- **aborted** - Upload cancelled
- **expired** - Upload session timed out

Key fields:

- `id` (UUID) - Primary key
- `tenant_id` - Multi-tenancy isolation
- `object_id` - References `objects.id` (CASCADE delete)
- `upload_id` - S3 multipart upload ID
- `part_size_bytes` - Size of each part
- `status` - Upload session state
- `expires_at` - Session expiration time

Indexes:

- `idx_mpu_tenant_created_at` - Query uploads by tenant and creation time
- `uq_mpu_tenant_upload` - Enforce unique upload IDs per tenant

#### `multipart_parts`

Tracks individual parts within a multipart upload (optional tracking):

Key fields:

- `multipart_id` - References `multipart_uploads.id` (CASCADE delete)
- `part_number` - Part sequence number
- `etag` - S3 ETag for verification
- `size_bytes` - Part size

Primary key: `(multipart_id, part_number)`

### Relationships

```text
objects (1) ──< (N) multipart_uploads ──< (N) multipart_parts
```

- One object can have multiple multipart upload sessions (e.g., retries)
- One multipart upload consists of multiple parts
- Cascade deletes ensure referential integrity

## Configuration

The service uses a CUE schema (`internal/config/schema.cue`) for validation. Configuration is loaded from [`configs/paladin.yaml`](configs/paladin.yaml).

### Logger

Controls structured logging output:

```yaml
logger:
  level: debug              # Log level: debug, info, warn, error, dpanic, panic, fatal
  format: json              # Output format: json (production) or console (development)
  development: false        # Enable development mode (DPanic causes panic)
  disable_caller: false     # Disable file/line number in logs
  disable_stacktrace: false # Disable stack traces on error logs
```

### Server

HTTP and gRPC server configuration:

```yaml
server:
  mode: release             # Gin mode: release, debug, test
  name: paladin # Service name
  http:
    addr: "0.0.0.0:8080"    # HTTP server bind address
  grpc:
    addr: "0.0.0.0:9090"    # gRPC server bind address
  shutdown_timeout: 20s     # Graceful shutdown timeout
  log_probes: false         # Log health check probe requests
```

### PostgreSQL

Database connection settings:

```yaml
postgres:
  dsn: "postgres://user:pass@host:5432/dbname?sslmode=disable"
```

The DSN (Data Source Name) includes:

- Username and password
- Host and port
- Database name
- SSL mode (disable for local dev, require for production)

### S3 Storage

S3-compatible storage configuration (AWS S3, SeaweedFS, MinIO):

```yaml
s3:
  bucket: paladin               # Default bucket name
  region: us-east-1         # AWS region
  endpoint: "http://seaweedfs-s3.storage.svc.cluster.local:8333" # S3 endpoint URL
  force_path_style: true    # Use path-style URLs (required for MinIO/SeaweedFS)
  access_key: dummy         # S3 access key
  secret_key: dummy         # S3 secret key
  presign_ttl: 15m          # Presigned URL expiration time
  part_size: "8MB"          # Multipart upload part size
```

**Notes:**

- `force_path_style: true` is required for non-AWS S3 implementations
- `presign_ttl` determines how long upload/download URLs remain valid
- `part_size` affects multipart upload performance (larger = fewer parts, smaller = more parallelism)

### Policy

Upload validation and restrictions:

```yaml
policy:
  max_object_size: "100MB"  # Maximum allowed object size
  allowed_content_types:    # Whitelist of allowed MIME types
    - "image/jpeg"
    - "image/png"
    - "application/pdf"
```

Objects exceeding `max_object_size` or with disallowed content types will be rejected.

### OpenTelemetry (Optional)

Distributed tracing and observability:

```yaml
otel:
  enabled: false            # Enable/disable OpenTelemetry
  service_name: paladin # Service identifier in traces
  environment: local        # Environment tag (local, dev, staging, prod)
  otlp_endpoint: "otel-collector:4317" # OTLP collector endpoint
  insecure: true            # Use insecure connection (disable TLS)
```

Set `enabled: true` to export traces to an OpenTelemetry collector.

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
