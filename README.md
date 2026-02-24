# paladin

A presign-only control plane for S3-compatible object storage (AWS S3 / SeaweedFS / MinIO). The service manages object metadata and lifecycle in PostgreSQL and issues time-limited presigned URLs — it never proxies or streams object data.

---

## How It Works

```mermaid
graph LR
    Client["Client\n(Browser / Service)"]
    PALADIN["Paladin\n(REST + gRPC)"]
    PG[("PostgreSQL\nMetadata")]
    S3[("S3-compatible\nStorage")]

    Client -->|"1. POST /v1/objects"| PALADIN
    PALADIN -->|"2. INSERT metadata"| PG
    PALADIN -->|"3. GeneratePresignedURL"| S3
    PALADIN -->|"4. Return presigned URL"| Client
    Client -->|"5. PUT file (direct)"| S3
    Client -->|"6. POST /v1/objects/:id/complete"| PALADIN
    PALADIN -->|"7. UPDATE status → complete"| PG
```

---

## Features

| Feature | Description |
|---------|-------------|
| **REST & gRPC APIs** | Dual transport: OpenAPI 3.0 (Gin) and Protocol Buffers |
| **Presigned URLs** | Time-limited upload/download URLs — no data proxying |
| **Multipart Uploads** | First-class support for large files (Part sign, Batch sign, Complete, Abort) |
| **Categories** | Server-side object grouping with CRUD management |
| **Multi-Tenancy** | Strict tenant isolation via `X-Tenant-ID` header; optional PostgreSQL RLS |
| **Rate Limiting** | Per-tenant token-bucket with automatic LRU cleanup |
| **Idempotency** | Safe retries via `Idempotency-Key` header (CREATE operations) |
| **Lifecycle GC** | Background Reaper for pending TTL cleanup, multipart abort, audit pruning |
| **LRU Cache** | 70–90% cache hit rate for read-heavy object metadata |
| **Observability** | 100% OTel tracing coverage, Prometheus metrics, structured Zap logging |
| **Schema-first Config** | CUE schema validation with smart defaults — bad config = hard exit |
| **Audit Logging** | Every API request logged with method, path, status, latency, actor |

---

## Tech Stack

| Layer | Technology |
|-------|-----------|
| HTTP Server | [Gin](https://gin-gonic.com/) |
| gRPC Server | [gRPC-Go](https://grpc.io/) |
| API Contract | [OpenAPI 3.0](https://www.openapis.org/) + [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) |
| Database | [PostgreSQL](https://www.postgresql.org/) via [pgx/v5](https://github.com/jackc/pgx) |
| Migrations | [golang-migrate](https://github.com/golang-migrate/migrate) |
| Config | [CUE](https://cuelang.org/) schema validation |
| Logging | [Zap](https://github.com/uber-go/zap) |
| Observability | [OpenTelemetry](https://opentelemetry.io/) + Prometheus |
| DI | [Wire](https://github.com/google/wire) |
| CLI | [Cobra](https://github.com/spf13/cobra) |
| Tests | [Ginkgo](https://onsi.github.io/ginkgo/) + [Gomega](https://onsi.github.io/gomega/), [Hurl](https://hurl.dev/) |

---

## Project Structure

```text
paladin/
├── api/                        # OpenAPI 3.0 specification (source of truth)
├── proto/paladin/v1/               # Protocol Buffer definitions
├── cmd/server/                 # Application entrypoint (Cobra CLI)
├── configs/                    # YAML configuration files
│   └── paladin.yaml
├── deploy/
│   ├── Dockerfile
│   └── kubernetes/
├── docs/
│   ├── API.md                  # REST + gRPC API reference
│   ├── configuration.md        # Configuration guide
│   └── database.md             # Schema, ERD, migrations
├── internal/
│   ├── api/
│   │   ├── http/               # HTTP handlers, middleware wiring, adapter
│   │   └── grpc/               # gRPC handlers and server setup
│   ├── app/                    # Wire-generated dependency injection
│   ├── breaker/                # Circuit breaker factory
│   ├── config/                 # CUE-powered config loading
│   │   ├── config.go
│   │   └── schema.cue          # CUE validation schema
│   ├── domain/                 # Core models and service interfaces
│   ├── generated/api/          # Auto-generated OpenAPI Go code
│   ├── logger/                 # Zap logger initialization
│   ├── middleware/             # HTTP middleware (auth, rate limit, audit, OTEL)
│   ├── observability/          # OTel trace/metric instrumentation
│   ├── service/                # Business logic implementation
│   ├── storage/                # S3 client abstraction
│   ├── store/                  # PostgreSQL repository implementations
│   └── utils/                  # Shared utilities
├── migrations/                 # SQL migration files (up + down)
└── tools.go                    # Tool dependencies (oapi-codegen, wire, etc.)
```

---

## Object Lifecycle

```mermaid
stateDiagram-v2
    [*] --> pending : POST /v1/objects
    pending --> uploaded : Client PUT to S3
    uploaded --> complete : POST /objects/{id}/complete
    complete --> soft_deleted : DELETE /objects/{id}
    soft_deleted --> complete : POST /objects/{id}/restore
    soft_deleted --> hard_deleted : DELETE /objects/{id}/purge
    complete --> hard_deleted : DELETE /objects/{id}/purge
    pending --> aborted : Reaper TTL
    aborted --> [*]
    hard_deleted --> [*]
```

---

## Quick Start

### Prerequisites

- Go 1.24+
- [Task](https://taskfile.dev/) (`brew install go-task`)
- Docker & Docker Compose
- PostgreSQL 15+

### Local Development

```bash
# Start dependencies (Postgres + SeaweedFS)
task up

# Apply database migrations
task migrate:up

# Run the service
task run

# Verify health
curl -s http://localhost:8080/health/readyz | jq .
```

### Testing

```bash
# Unit tests (Ginkgo)
task test

# Integration tests (Hurl)
task test:hurl

# Or directly
go test -v ./internal/...
```

### Build

```bash
# Build binary
task build

# Build Docker image
task docker:build
```

---

## API Endpoints

See [docs/API.md](./docs/API.md) for the full reference.

| Group | Endpoints |
|-------|-----------|
| **Health** | `GET /health/livez`, `/health/startupz`, `/health/readyz`, `/version`, `/metrics` |
| **Objects** | `POST /v1/objects`, `GET /v1/objects`, `GET /v1/objects/:id`, `PATCH /v1/objects/:id/meta`, `DELETE /v1/objects/:id`, `DELETE /v1/objects/:id/purge`, `POST /v1/objects/:id/complete`, `POST /v1/objects/:id/restore` |
| **Multipart** | `POST /v1/multipart`, `POST /v1/multipart/:id/parts/:n/sign`, `POST /v1/multipart/:id/complete`, `POST /v1/multipart/:id/abort` |
| **Categories** | `GET /v1/categories`, `POST /v1/categories`, `DELETE /v1/categories/:slug` |
| **Admin** | `GET /admin/config`, `GET /admin/audit-logs`, `GET /admin/audit-logs/:id` |
| **Ops** | `GET /v1/ops/stats`, `GET /v1/ops/s3/ping` |

---

## Configuration

Configuration is loaded from `configs/paladin.yaml` and validated against `internal/config/schema.cue`.

See [docs/configuration.md](./docs/configuration.md) for the full guide.

Key settings:

```yaml
app:
  name: paladin
  env: local

datastores:
  postgres:
    dsn: "postgres://user:pass@host:5432/db"
  s3:
    bucket: my-bucket
    endpoint: "https://s3.amazonaws.com"

policy:
  max_object_size: "100MB"
  min_part_size: "5MB"
```

---

## Database

See [docs/database.md](./docs/database.md) for the full schema reference.

Tables: `objects`, `multipart_uploads`, `multipart_parts`, `categories`, `audit_logs`, `idempotency_keys`

```bash
# Apply migrations
task migrate:up

# Rollback one step
task migrate:down
```

---

## Architecture Notes

- **No data proxying**: The service only manages metadata and presigned URLs. Object data flows directly between the client and S3.
- **Interface-first**: All service methods are defined as Go interfaces in `internal/domain/`, keeping business logic testable.
- **Circuit breakers**: Applied on all S3 and database operations via a central factory.
- **Graceful shutdown**: 20 s drain period for in-flight requests on SIGTERM/SIGINT.
- **Health probes**: `readyz` performs `HeadBucket` on S3 — if S3 is unreachable, the pod is removed from load balancer rotation.

See [docs/architecture.md](./docs/architecture.md) for the full layered architecture with diagrams.

---

## Documentation

| Doc | Description |
|-----|-------------|
| [docs/architecture.md](./docs/architecture.md) | Layered architecture, middleware stack, FSMs, DI graph, Reaper |
| [docs/API.md](./docs/API.md) | REST + gRPC API reference with sequence diagrams |
| [docs/database.md](./docs/database.md) | Database schema, ERD, migrations, RLS |
| [docs/configuration.md](./docs/configuration.md) | All configuration settings with examples |
