# Paladin (PALADIN)

**Paladin** is a multi-tenant object lifecycle management service. It acts as a control plane for S3-compatible storage: it manages object metadata, orchestrates pre-signed upload/download URLs, tracks multipart uploads, enforces tenant isolation, and handles lifecycle housekeeping. Binary data never passes through this service — only metadata and pre-signed URL coordination.

## Navigation

| Document | Description |
|---|---|
| [Architecture](docs/architecture.md) | Service components, runtime entry points, and how everything wires together |
| [Configuration](docs/configuration.md) | All configuration keys, their defaults, and environment variable mapping |
| [API](docs/API.md) | Complete HTTP REST and gRPC API reference |
| [Database](docs/database.md) | PostgreSQL schema, RLS policies, indexes, and migration history |
| [Logging](docs/logging.md) | Structured logging setup, fields, and example log entries |
| [Telemetry](docs/telemetry.md) | Prometheus metrics catalog, OpenTelemetry tracing, and health probes |
| [Async & Jobs](docs/async.md) | Background reaper worker: pending objects, multipart uploads, audit log pruning |
| [Security](docs/security.md) | Authentication, authorization, tenant isolation, and security headers |
| [Diagrams](docs/diagrams.md) | C4 context, sequence, ER, and deployment diagrams |
| [Operations](docs/operations.md) | Running locally, building Docker image, and Kubernetes deployment |

## Technology Stack

| Component | Technology |
|---|---|
| Language | Go 1.23+ |
| HTTP Framework | Gin (`github.com/gin-gonic/gin`) |
| gRPC | `google.golang.org/grpc` |
| CLI / Bootstrap | Cobra |
| Dependency Injection | Google Wire |
| Database | PostgreSQL (pgx/v5 + pgxpool) |
| Object Storage | S3-compatible (SeaweedFS in local/staging) |
| Migrations | Goose |
| Logging | Zap (structured, JSON) |
| Metrics | Prometheus (`promauto` + default Go runtime collector) |
| Tracing | OpenTelemetry (OTLP gRPC/HTTP exporter) |
| Config | YAML + Kubernetes Secrets |
| Build | Taskfile |
| Container | Multi-stage Dockerfile |
