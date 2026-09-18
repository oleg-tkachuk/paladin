# Operations

## Running Locally

### Prerequisites

- Go 1.23+
- Task (`brew install go-task/tap/go-task`)
- Docker + Docker Compose
- A running PostgreSQL instance (or use the provided compose file)
- A running SeaweedFS instance (or MinIO)

### Start with Docker Compose

```bash
cd deploy/
docker-compose up -d
```

Compose services:

- PostgreSQL with custom config (`postgres-custom.conf`)
- SeaweedFS S3-compatible storage (configured via `seaweedfs-s3.json`)

### Local Config

Use `configs/local.yaml` for local development:

```bash
./server --config configs/local.yaml
```

### Run Migrations

Migrations use Goose. From the project root:

```bash
go run github.com/pressly/goose/v3/cmd/goose@latest -dir migrations postgres "DSN_HERE" up
```

In a cluster this is the chart's `migrate` job, which runs before the
deployments are patched — there is no Task target for it.

### Build and Run

```bash
# Build + push image and Helm chart to the OCI registry
task deploy

# Or build into the local Docker store + helm install to OrbStack (no registry)
task deploy-local

# Run the binary directly
./server --config configs/local.yaml
```

### Run Tests

```bash
# Unit tests
task test

# Integration tests (require running DB and S3)
task test:integration
```

## Docker Build

**Source:** `deploy/Dockerfile`

Multi-stage build:

1. Builder stage — compiles the Go binary
2. Final stage — minimal image, copies binary + config

```bash
docker build -f deploy/Dockerfile -t paladin-core:dev .
```

Default runtime config path inside the image: `/app/configs/config.yaml`

## Taskfile Commands

Available tasks (from `Taskfile.yaml`):

```bash
task --list
```

Common tasks include:

- `task deploy` — build + push image and Helm chart
- `task deploy-local` — build into the local Docker store + helm install (no registry)
- `task test` — run unit tests
- `task lint` — run golangci-lint
- `task codegen:proto` — regenerate proto Go files
- `task codegen:sqlc` — regenerate sqlc query files

## Kubernetes Deployment

In Kubernetes, the service is deployed with:

- A `Deployment` running the compiled binary
- A `ConfigMap` mounting the YAML config to `/app/configs/config.yaml`
- `Secret` objects referenced in the config for DB password and S3 credentials
- Two `Services`: one for HTTP (`:8080`), one for Connect (`:9090`)
- Liveness probe: `GET /health/livez`
- Readiness probe: `GET /health/readyz`
- Startup probe: `GET /health/startupz`

For Helm chart details, refer to [`deploy/chart/`](../deploy/chart/).

## Source Index

| File | Description |
|---|---|
| `cmd/server/main.go` | Binary entry point — calls `Execute()` |
| `cmd/server/root.go` | Cobra root command — flag parsing, app initialization, signal handling |
| `cmd/server/wire.go` | Wire provider declarations |
| `cmd/server/wire_gen.go` | Wire-generated dependency injection graph |
| `internal/config/types.go` | All config struct definitions |
| `internal/config/config.go` | Config loader (YAML → struct) |
| `internal/config/resolver.go` | Kubernetes secret resolver |
| `internal/config/schema.cue` | CUE schema for config validation |
| `internal/api/connect/server.go` | Connect server implementation |
| `internal/api/connect/tenant_types.go` | Connect tenant RPC message mappers |
| `internal/api/connect/validation.go` | Connect request validation logic |
| `internal/api/http/router.go` | Gin HTTP router setup |
| `internal/api/http/adapter.go` | OpenAPI → domain service adapter (all HTTP handler implementations) |
| `internal/middleware/http_stack.go` | HTTP middleware chain setup |
| `internal/middleware/connect_chain.go` | Connect interceptor chain setup |
| `internal/middleware/auth.go` | HTTP tenant enforcement middleware |
| `internal/middleware/ratelimit.go` | Per-tenant token bucket rate limiter |
| `internal/middleware/audit_log.go` | HTTP audit logging middleware |
| `internal/middleware/security_headers.go` | Security response headers |
| `internal/middleware/request_size_limit.go` | Request body size enforcement |
| `internal/service/object_service.go` | Core object lifecycle service |
| `internal/service/object_tag_service.go` | ObjectTag management service |
| `internal/service/health.go` | Health & dependency check service |
| `internal/service/system_service.go` | Admin config endpoint service |
| `internal/service/policy.go` | Upload policy enforcement |
| `internal/store/` | PostgreSQL repositories (sqlc-generated queries + wrappers) |
| `internal/storage/` | S3 client adapter |
| `internal/worker/reaper.go` | Background reaper goroutine |
| `internal/metrics/metrics.go` | Prometheus metric definitions |
| `internal/metrics/otel.go` | OpenTelemetry instrument definitions |
| `migrations/001_init.sql` | Initial schema: objects, multipart_uploads, multipart_parts |
| `migrations/003_harden_objects.sql` | RLS policies, triggers, CHECK constraints |
| `migrations/004_v1_1_0_refactor.sql` | Status expansion, idempotency_keys table |
| `migrations/007_audit_logs.sql` | audit_logs table |
| `migrations/010_object_tag_support.sql` | object_object_tags table, object_tag/subpath columns |
| `proto/paladin.proto` | Connect service and message definitions |
| `configs/config.yaml` | Default config (local environment) |
| `deploy/Dockerfile` | Multi-stage Docker build |
| `deploy/docker-compose.yaml` | Local development stack |
