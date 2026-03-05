# Architecture

## Overview

Paladin (PALADIN) is a **pure control-plane service**: it manages lifecycle metadata for objects stored in S3-compatible storage. Binary payloads never traverse PALADIN; instead, callers receive pre-signed URLs to upload/download directly from S3.

## Folder Structure

```
paladin/
├── cmd/server/            # Entrypoint (Cobra CLI, Wire injection)
├── internal/
│   ├── api/
│   │   ├── grpc/          # gRPC server + generated pb Go code
│   │   └── http/          # Gin HTTP server: router, OpenAPI adapter
│   ├── app/               # App struct: runs gRPC + HTTP servers, manages lifecycle
│   ├── breaker/           # Circuit breakers (gobreaker)
│   ├── cache/             # In-process LRU category cache
│   ├── config/            # Config types, CUE schema validation, resolver
│   ├── domain/            # Interfaces (ports): ObjectsService, CategoryService, Repos, etc.
│   ├── errors/            # Domain error types + HTTP/gRPC mapping
│   ├── generated/api/     # oapi-codegen generated types + server interface
│   ├── logger/            # Zap logger factory
│   ├── metrics/           # Prometheus metrics + OTel histograms/counters
│   ├── middleware/         # HTTP middleware stack + gRPC interceptor chain
│   ├── observability/     # OTel provider setup
│   ├── openapi/           # OpenAPI spec embedding
│   ├── service/           # Business logic (ObjectsService, CategoryService, HealthService, etc.)
│   ├── storage/           # S3 client adapter (aws-sdk-go-v2)
│   ├── store/             # PostgreSQL repositories (sqlc-generated queries + wrappers)
│   ├── utils/             # Helpers: context values (TenantID, RequestID), size parsing, etc.
│   ├── wire/              # Wire providers
│   └── worker/            # Background Reaper goroutine
├── migrations/            # Goose SQL migrations (013 files)
├── proto/                 # paladin.proto + generated Go code
├── configs/               # YAML config files
├── deploy/                # Dockerfile, docker-compose, env files
└── tests/                 # Integration tests
```

## Service Boundaries

### Internal Components

| Component | Description |
|---|---|
| `cmd/server` | Bootstrap: parses flags, reads config, runs Wire injection, starts servers |
| `internal/service` | Core business logic: object lifecycle, multipart, categories, health |
| `internal/store` | PostgreSQL query layer (sqlc + pgxpool) |
| `internal/storage` | S3 client abstraction (pre-sign, multipart management) |
| `internal/api/http` | REST API server (Gin + oapi-codegen adapter) |
| `internal/api/grpc` | gRPC server implementing the `Paladin` proto service |
| `internal/worker` | Reaper goroutine for expired objects and multipart cleanup |

### External Dependencies

| Dependency | Purpose |
|---|---|
| PostgreSQL | Object/category/audit metadata, RLS-based tenant isolation |
| S3-compatible storage (SeaweedFS) | Binary object storage; pre-signed URL generation |
| Kubernetes Secrets | Password and access-key injection at runtime |
| OpenTelemetry Collector (optional) | Trace/metric export via OTLP |

## Runtime Entry Points

### Bootstrap Sequence (`cmd/server/root.go`)

1. Cobra command parses `--config` flag (default `/app/configs/paladin.yaml`)
2. `InitializeApp()` (Wire-generated) constructs the entire dependency graph:
   - `ProvideConfig` → loads and validates YAML + Kubernetes Secrets
   - `ProvideLogger` → creates a Zap logger
   - `ProvideDB` → opens a `pgxpool` connection pool
   - `ProvideObjectsRepo`, `ProvideMultipartRepo`, `ProvideCategoryRepo`, `ProvideIdempotencyRepo`, `ProvideAuditRepo`
   - `ProvideS3` → creates an S3 client (AWS SDK v2)
   - `ProvidePolicy` → validates policy constraints
   - `ProvideBreakerFactory` → circuit breaker factory (gobreaker)
   - `ProvideObjectsService`, `ProvideCategoryService`, `ProvideHealthService`, `ProvideSystemService`
   - `ProvideGRPCServer` → gRPC `Paladin` server
   - `ProvideHTTPServer` → Gin HTTP server with full middleware stack
   - `ProvideOTel` → OTel tracer/meter provider (optional)
   - `ProvideReaper` → background cleanup goroutine
3. `app.Run()` starts gRPC + HTTP servers concurrently (goroutines)
4. SIGINT/SIGTERM triggers graceful shutdown (configurable `shutdown_timeout`)

### Servers

| Server | Default Address | Protocol |
|---|---|---|
| HTTP REST + Prometheus + Health | `0.0.0.0:8080` | HTTP/1.1 (TLS optional) |
| gRPC | `0.0.0.0:9090` | HTTP/2 (TLS optional) |

### Goroutines

| Goroutine | Owner | Lifetime |
|---|---|---|
| HTTP server | `app.Run` | Until shutdown |
| gRPC server | `app.Run` | Until shutdown |
| Reaper cleanup | `worker.Reaper.Start` | Until context cancelled |
| Rate-limiter cleanup | `middleware.TenantRateLimiter.periodicCleanup` | Process lifetime |

## Dependency Injection

Wire providers are in `internal/wire/` and compiled output is in `cmd/server/wire_gen.go`.
