# paladin

Presign-only control plane for S3-compatible object storage (AWS S3 / SeaweedFS / MinIO-compatible).

## Features

- **Metadata Management**: Atomically tracks object metadata and multipart upload states in PostgreSQL.
- **Secure Access**: Generates time-limited presigned URLs for single-part and multipart uploads/downloads.
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
```

### Health Probes

Exposes standard endpoints following Kubernetes best practices:

- `GET /health/livez` - Liveness probe (Restart logic). Checks if process is responsive.
- `GET /health/startupz` - Startup probe (Initialization). Checks if config and clients are ready.
- `GET /health/readyz` - Readiness probe (Traffic). Checks connectivity to Postgres and SeaweedFS.

**Config**: `server.log_probes: true|false` (Toggles probe logging).

## Local Development

```bash
# Build the server binary and Docker image
task build

# Start the full stack (Postgres + SeaweedFS + PALADIN)
task up

# Verify the service is ready
curl -s http://localhost:8080/health/readyz | jq .
```

## API Endpoints

### HTTP API

- `GET /health/livez` - Liveness probe
- `GET /health/readyz` - Readiness probe
- `GET /version` - Version information
- `GET /metrics` - Prometheus metrics

### gRPC API

- `Paladin/PresignPut`
- `Paladin/PresignGet`
- `Paladin/CreateMultipartUpload`
- `Paladin/PresignUploadPart`
- `Paladin/CompleteMultipartUpload`
- `Paladin/AbortMultipartUpload`

## Notes

- **Data Flow**: This service intentionally does not proxy or stream object data. It only manages metadata and signs access URLs.
- **SeaweedFS**: For local development, dummy credentials are used as the service defaults to static client credentials.
