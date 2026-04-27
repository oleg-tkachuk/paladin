# Paladin (PALADIN)

## Overview

The Paladin (PALADIN) is a high-performance service for managing object metadata and lifecycle across S3-compatible storage systems. It provides multi-tenant isolation, structured audit logs, and a standardized API for object interactions.

## Documentation

For detailed implementation details, see:

- [Architecture](docs/architecture.md) — High-level overview, topology, and source index.
- [API](docs/API.md) — Connect RPC service definitions, filtering, and validation.
- [Configuration](docs/configuration.md) — YAML/Env/Secret configuration settings.
- [Database](docs/database.md) — Schema, tables, and data lifecycle (soft vs hard delete).
- [Logging](docs/logging.md) — Structured logging and event taxonomy.
- [Telemetry](docs/telemetry.md) — Metrics (RED) and tracing (OTEL).
- [Security](docs/security.md) — Tenant isolation, RBAC, and secret management.
- [Operations](docs/operations.md) — Deployment, health checks, and Taskfile usage.
- [Async & Jobs](docs/async.md) — Background workers (Reaper).
- [Diagrams](docs/diagrams.md) — Mermaid diagrams of context and architecture.

## Getting Started

### Prerequisites

- Go 1.21+
- PostgreSQL 15+
- S3-compatible storage (e.g., SeaweedFS, MinIO, AWS S3)

### Running Locally

1. Copy the example config: `cp configs/paladin.yaml.example configs/paladin.yaml`
2. Start dependencies (e.g., via Docker Compose).
3. Run the migrations: `task db:migrate`
4. Start the server: `go run cmd/server/main.go`

## Infrastructure

- **K8s Resources**: Deployments, Services, ConfigMaps, Secrets, Ingress.
- **Helm Values**: See [deploy/helm/values.yaml](deploy/helm/values.yaml).
- **CI/CD**: GitHub Actions for building images and running tests.

## Source Index

- [cmd/server/main.go](cmd/server/main.go) - Entry point.
- [internal/api/](internal/api/) - Layered API implementations.
- [internal/service/](internal/service/) - Domain services.
- [internal/store/](internal/store/) - Postgres repositories.
