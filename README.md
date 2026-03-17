# Paladin (PALADIN)

## Overview

The Paladin (PALADIN) is a high-performance service for managing object metadata and lifecycle across S3-compatible storage systems. It provides multi-tenant isolation, structured audit logs, and a standardized API for object interactions.

## Documentation Index

- [Architecture](docs/architecture.md) - High-level design and inventory.
- [Configuration](docs/configuration.md) - Environment variables and YAML settings.
- [API](docs/api.md) - REST and gRPC/Connect endpoints.
- [Database](docs/database.md) - Postgres schema and migrations.
- [Logging](docs/logging.md) - Structured logging and event taxonomy.
- [Telemetry](docs/telemetry.md) - Metrics and tracing.
- [Async & Jobs](docs/async.md) - Reaper worker and background tasks.
- [Security](docs/security.md) - Auth, isolation, and secret management.
- [Diagrams](docs/diagrams.md) - Visualizing flows and architecture.

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
- **Helm Values**: See [deploy/helm/values.yaml](file:///workspace/deploy/helm/values.yaml).
- **CI/CD**: GitHub Actions for building images and running tests.

## Source Index

- [cmd/server/main.go](file:///workspace/cmd/server/main.go) - Entry point.
- [internal/api/](file:///workspace/internal/api/) - Layered API implementations.
- [internal/service/](file:///workspace/internal/service/) - Domain services.
- [internal/store/](file:///workspace/internal/store/) - Postgres repositories.
