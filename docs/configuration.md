# Configuration - Paladin (PALADIN)

## Overview

PALADIN uses [Koanf](https://github.com/knadh/koanf) to load and merge configuration from multiple sources. Validation is enforced via [CUE](https://cuelang.org/).

## Configuration Sources

- **YAML**: `configs/paladin.yaml`
- **Environment**: Prefixed with `PALADIN_`. Nested fields use underscores (e.g., `PALADIN_DATASTORES_POSTGRES_DSN`).
- **K8s Secrets**: If running in Kubernetes (`KUBERNETES_SERVICE_HOST` is set), PALADIN resolves secrets in the current namespace.

## Key Configuration Sections

### 1. App & Server

- `app.env`: Execution environment (development, production).
- `server.http.addr`: Address for the HTTP/REST server (default: `:8080`).
- `server.grpc.addr`: Address for the gRPC/Connect server (default: `:8081`).

### 2. Datastores

- **Postgres**: Connection DSN and pool settings. Supports `password_secret` for K8s integration.
- **S3**: Configuration for S3-compatible storage (endpoint, bucket, region, access/secret keys). Supports `secret` suffixes for K8s.

### 3. Policy

- `max_object_size`: Maximum allowed size for a single object.
- `max_multipart_size`: Maximum allowed size for a multipart upload.
- `allowed_content_types`: List of permitted MIME types.

### 4. Housekeeping

- `enable_reaper`: Enables the background GC worker.
- `gc_interval`: Frequency of reaper executions (default: `1h`).
- `pending_ttl`: How long to keep `pending` objects before purging.

## Schema Reference

The full configuration schema is defined in [internal/config/schema.cue](file:///workspace/internal/config/schema.cue).
