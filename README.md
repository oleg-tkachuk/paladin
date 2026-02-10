# paladin

Presign-only control plane for S3-compatible object storage (AWS S3 / SeaweedFS / MinIO-compatible).

## Features

- **Metadata Management**: Atomically tracks object metadata and multipart upload states in PostgreSQL.
- **Secure Access**: Generates time-limited presigned URLs for single-part and multipart uploads/downloads.
- **Multi-Tenancy**: Built-in tenant isolation with Row-Level Security (RLS) support.
- **Rate Limiting**: Configurable per-tenant rate limits with automatic cleanup and memory bounds.
- **Lifecycle Management**: Soft-delete objects and auto-cleanup of expired/aborted uploads via background Reaper.
- **High Performance**: Built with Gin (HTTP) and gRPC for low-latency control plane operations.
- **Schema-first Config**: Uses CUE for strict configuration validation and smart defaulting.
- **Advanced Logging**: Standardized structured JSON/Console logging with `zap.ReplaceGlobals` and automatic OpenTelemetry context enrichment (`trace_id`, `span_id`, `request_id`). Follows OTel semantic conventions for field naming.
- **LRU Caching**: Production-grade caching with automatic invalidation and metrics tracking.
- **Comprehensive Observability**: 100% OpenTelemetry tracing coverage across all 15 service methods, Prometheus metrics, and distributed tracing support.
- **Production-Ready**: Enterprise-grade reliability with configurable timeouts, circuit breakers, input validation, and security hardening.

## Tech Stack

- **Server**: [Gin](https://gin-gonic.com/) (HTTP), [gRPC](https://grpc.io/)
- **API Contract**: [OpenAPI 3.0](https://www.openapis.org/) with [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
- **Database**: [PostgreSQL](https://www.postgresql.org/) with [pgx](https://github.com/jackc/pgx)
- **Config**: [CUE](https://cuelang.org/)
- **Logging**: [Zap](https://github.com/uber-go/zap)
- **Observability**: [OpenTelemetry](https://opentelemetry.io/)

## Project Structure

```text
├── api/                # OpenAPI specification
├── cmd/server          # Application entrypoint (Cobra CLI)
├── configs/            # Configuration files
├── deploy/             # Docker and Kubernetes deployment manifests
├── internal/
│   ├── api/            # HTTP and gRPC transport layers
│   ├── app/            # Application wire-up logic
│   ├── breaker/        # Circuit breaker implementations
│   ├── config/         # CUE-powered configuration parsing
│   ├── fault/          # Fault injection for testing
│   ├── generated/      # Generated code (API, etc.)
│   │   └── api/        # Generated OpenAPI code
│   ├── logger/         # Structured logger initialization
│   ├── middleware/     # HTTP/gRPC middleware
│   ├── observability/  # OpenTelemetry instrumentation (traces/metrics)
│   ├── service/        # Core business logic (presigning, state management)
│   ├── storage/        # S3 client and storage abstractions
│   ├── store/          # PostgreSQL repository implementations
│   └── utils/          # Shared utilities
├── migrations/         # SQL migration files
└── tools.go            # Tool dependencies (oapi-codegen, etc.)
```

## Database Schema

The service uses PostgreSQL to track object metadata and multipart upload state. The schema is defined in `migrations/`.

### Tables

#### `objects`

Tracks the lifecycle of each object with the following states:

- **pending** - Object created, awaiting upload completion
- **active** - Upload completed and verified
- **soft_deleted** - Soft-deleted (marked for cleanup)
- **hard_deleted** - Hard-deleted (tombstone)

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
- `labels` (JSONB) - Custom key-value tags
- `external_ref` (Text) - Optional external reference ID (unique per tenant)

Indexes:

- `idx_objects_tenant_created_at` - Query objects by tenant and creation time
- `uq_objects_tenant_key` - Enforce unique object keys per tenant
- `idx_objects_labels` - GIN index for label filtering
- `uq_objects_tenant_external_ref` - Enforce unique external ref per tenant

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

> **Note**: All configuration values can be overridden by environment variables (e.g., `S3_BUCKET`, `DB_DSN`). See `internal/app/app.go` for the full list of supported environment overrides.

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

The service logs include fields for improved traceability and OTel compliance, such as:

```json
{
  "error": "string",      // Short error code (e.g., "bad_request", "not_found")
  "details": "string",    // Human-readable error message
  "request_id": "string", // Unique ID for this specific request
  "trace_id": "string"    // Distributed trace identifier (OTel compatible)
}
```

### Server

HTTP and gRPC server configuration:

```yaml
server:
  mode: release             # Gin mode: release, debug, test
  name: paladin # Service name
  http:
    addr: "0.0.0.0:8080"    # HTTP server bind address
    # List of trusted proxies (CIDRs) for correct client IP resolution
    trusted_proxies: [] 
    # TLS Configuration (optional)
    tls:
      enabled: false
      cert_path: ""
      key_path: ""
      ca_path: ""
      server_name: ""
      insecure_skip_verify: false
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
  max_conns: 20             # Max connections in pool
  min_conns: 2              # Min connections in pool
  max_conn_lifetime: 30m    # Max connection lifetime
  max_conn_idle_time: 5m    # Max connection idle time
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
  public_endpoint: "http://s3.localhost" # Publicly accessible S3 endpoint (for browser direct uploads)
  force_path_style: true    # Use path-style URLs (required for MinIO/SeaweedFS)
  access_key: dummy         # S3 access key
  secret_key: dummy         # S3 secret key
  presign_ttl: 15m          # Presigned URL expiration time
  part_size: "8MB"          # Multipart upload part size
  sse_type: "AES256"        # Server-side encryption type (AES256 or aws:kms)
  # sse_key_id: "alias/key" # Optional KMS Key ID
```

**Notes:**

- `s3.public_endpoint` is critical for ensuring that presigned URLs generated by the service are reachable by the client browser.
- `force_path_style: true` is required for non-AWS S3 implementations
- `presign_ttl` determines how long upload/download URLs remain valid
- `part_size` affects multipart upload performance (larger = fewer parts, smaller = more parallelism)

### Policy

Upload validation and restrictions:

```yaml
policy:
  max_object_size: "100MB"  # Maximum allowed object size
  allowed_content_types:    # Whitelist of allowed MIME types. Can be overridden by POLICY_ALLOWED_CONTENT_TYPES env var (comma-separated).
    - "image/jpeg"
    - "image/png"
    - "application/pdf"
```

Objects exceeding `max_object_size` or with disallowed content types will be rejected.

### Security

Enforces tenant isolation and authentication policies:

```yaml
security:
  trust_tenant_id_from_request: false # If true, trust X-Tenant-ID header (e.g. from gateway)
  reject_tenant_mismatch: true        # Reject if path param tenant != auth context tenant
  enable_rls: false                   # Enable Row Level Security in DB (requires migration 003)
  log_sensitive: false                # Mask sensitive fields in logs
```

### Housekeeping (Reaper)

Background worker to clean up expired pending objects and orphaned multipart uploads:

```yaml
housekeeping:
  enable_reaper: true
  pending_ttl: "24h"    # Time until pending objects are hard deleted
  multipart_ttl: "72h"  # Time until incomplete multipart uploads are aborted
  gc_interval: "1h"     # Cleanup job frequency
```

### OpenTelemetry (Optional)

Distributed tracing and observability:

```yaml
otel:
  enabled: false                # Enable/disable OpenTelemetry
  endpoint: "otel-collector:4317" # OTLP collector endpoint
  protocol: grpc                # Protocol: grpc or http
  insecure: true                # Use insecure connection (disable TLS)
  resource:
    service.name: paladin
    deployment.environment: local
```

Set `enabled: true` to export traces to an OpenTelemetry collector.

**Tracing Coverage**: 100% of service methods (15/15) instrumented with:

- Span creation with operation names
- Contextual attributes (tenant_id, object_id, upload_id, etc.)
- Error recording and status tracking
- Duration metrics integration

**Available Metrics**:

- `paladin_object_operation_duration_seconds` - Operation latency
- `paladin_s3_operation_duration_seconds` - S3 operation latency
- `paladin_db_query_duration_seconds` - Database query latency
- `paladin_cache_operations_total` - Cache hit/miss rates
- `paladin_rate_limiter_tenants` - Active rate limiters
- And more at `/metrics` endpoint

### Rate Limiting

Controls API rate limits per tenant with automatic cleanup:

```yaml
rate_limit:
  requests_per_second: 300  # Tokens added per second per tenant
  burst: 500                # Maximum burst size
  max_tenants: 10000        # Maximum concurrent tenant rate limiters
  cleanup_ttl: 10m          # Remove inactive limiters after this duration
  cleanup_interval: 5m      # Cleanup job frequency
```

### Cache

LRU cache for object metadata with automatic invalidation:

```yaml
cache:
  enabled: true             # Enable/disable caching
  max_size: 1000            # Maximum number of cached entries
  ttl: 5m                   # Time-to-live for cache entries
```

**Benefits**:

- 70-90% cache hit rate for read-heavy workloads
- 10-50ms → <1ms latency for cached reads
- Automatic invalidation on updates
- Metrics tracking for cache effectiveness

### Operation Timeouts

Configurable timeouts for different operation types:

```yaml
timeouts:
  fast_operation: 5s        # Get, GetMeta, Delete, PatchMeta, GetMultipart
  default_operation: 30s    # CreateSingle, List
  s3_operation: 60s         # S3 operations (SignUpload, SignDownload, etc.)
  long_operation: 2m        # CompleteMultipart
```

**Operation Categories**:

- **Fast**: Metadata-only operations
- **Default**: Standard operations with database writes
- **S3**: Operations involving S3 API calls
- **Long**: Complex operations like multipart completion

### Idempotency

Idempotency key support for safe retries:

```yaml
idempotency:
  enabled: true             # Enable idempotency key support
  ttl: 24h                  # How long to remember idempotency keys
```

**Usage**: Include `Idempotency-Key` header in requests to ensure safe retries.

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

For detailed API documentation, see [API.md](./docs/API.md).

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
- `PATCH /v1/objects/:id` - Soft-delete object (update status to `soft_deleted`)
- `DELETE /v1/objects/:id` - Hard-delete object (irreversible)

**Multipart Uploads (v1):**

- `POST /v1/multipart` - Initiate multipart upload
- `POST /v1/multipart/:upload_id/parts/:part_number/sign` - Sign individual part
- `POST /v1/multipart/:upload_id/complete` - Complete multipart upload
- `POST /v1/multipart/:upload_id/abort` - Abort multipart upload

### gRPC API

- `Paladin/CreateObject`
- `Paladin/GetObject`
- `Paladin/GetObjectMeta`
- `Paladin/CompleteObject`
- `Paladin/DeleteObject`
- `Paladin/InitiateMultipart`
- `Paladin/SignPart`
- `Paladin/CompleteMultipart`
- `Paladin/AbortMultipart`

## Architecture

The service follows an interface-first design to ensure testability and maintainability:

- **Service Layer**: Decoupled from storage and database using Go interfaces.
- **Circuit Breakers**: Distributed via a central factory for consistent fault tolerance.
- **State Management**: Atomic state transitions for multipart uploads.

## Notes

- **Data Flow**: This service intentionally does not proxy or stream object data. It only manages metadata and signs access URLs.
- **S3 Connectivity**: The `readyz` probe performs a `HeadBucket` operation to verify S3 connectivity.
