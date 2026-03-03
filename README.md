# Paladin

The **Paladin** is a robust, multi-tenant microservice acting as a metadata broker and orchestration layer over S3-compatible blob storage. It does not store binary blobs itself; instead, it tracks object lifecycles in PostgreSQL and issues secure, time-bound pre-signed URLs directly to clients for uploading and downloading content.

## Features

- **Decoupled Storage**: Clients upload binary data directly to S3 using short-lived tokens, bypassing the control plane to save bandwidth.
- **Strict Multi-Tenancy**: Data isolation is enforced at the database layer using PostgreSQL Row-Level Security (RLS).
- **Multipart Upload Support**: Full orchestration for files up to 5TB via S3 multipart APIs.
- **Idempotency**: Built-in support for safely retrying state-mutating requests.
- **Audit Logging**: Immutable history of all read/write actions.

## Documentation Navigation

Comprehensive technical documentation is split into domain-specific guides:

- **[Architecture](docs/architecture.md)**: System design, boundaries, and codebase inventory.
- **[Configuration](docs/configuration.md)**: Exhaustive breakdown of YAML configurations and environment mappings.
- **[API](docs/api.md)**: Endpoints, contracts, and interaction patterns.
- **[Database](docs/database.md)**: PostgreSQL schemas, RLS policies, and migrations.
- **[Logging](docs/logging.md)**: Log taxonomy, structured fields, and redaction policies.
- **[Telemetry](docs/telemetry.md)**: Prometheus metrics, OpenTelemetry spans, and Kubernetes health probes.
- **[Async & Jobs](docs/async.md)**: Background workers (e.g., the Reaper for garbage collection).
- **[Security](docs/security.md)**: Tenant isolation, authentication rules, and secret resolution.
- **[Operations](docs/operations.md)**: Docker packaging, build pipelines, and Kubernetes deployment.
- **[Diagrams (Mermaid)](docs/diagrams.md)**: Visual context, sequence, ER, and deployment diagrams.

## Quick Start

To run the full stack locally:

```bash
task up
```

Ensure you have your environment configuration securely populated before starting.
