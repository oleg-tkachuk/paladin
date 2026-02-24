# Configuration Guide

The Paladin reads a single YAML file validated at startup against a [CUE schema](../internal/config/schema.cue). Invalid config causes a hard exit with a descriptive error — no silent misconfigurations.

---

## Table of Contents

- [Configuration System](#configuration-system)
- [Structure Overview](#structure-overview)
- [app — Application Identity](#app--application-identity)
- [logger — Structured Logging](#logger--structured-logging)
- [otel — OpenTelemetry](#otel--opentelemetry)
- [server — HTTP & gRPC](#server--http--grpc)
- [datastores — PostgreSQL & S3](#datastores--postgresql--s3)
- [policy — Object Policies](#policy--object-policies)
- [security — Tenant Isolation](#security--tenant-isolation)
- [timeouts — Operation Timeouts](#timeouts--operation-timeouts)
- [rate_limit — Per-Tenant Rate Limits](#rate_limit--per-tenant-rate-limits)
- [cache — Metadata Cache](#cache--metadata-cache)
- [idempotency — Safe Retries](#idempotency--safe-retries)
- [housekeeping — Background Reaper](#housekeeping--background-reaper)
- [Secret Management](#secret-management)
- [Environment-Specific Files](#environment-specific-files)

---

## Configuration System

```mermaid
flowchart LR
    yaml["configs/*.yaml\nYAML File"]
    cue["internal/config/schema.cue\nCUE Schema"]
    validate{"Validate &\nApply Defaults"}
    json["JSON\nIntermediate"]
    go["Config struct\nGo Type-safe Access"]
    app["Application\nRuntime"]

    yaml --> validate
    cue --> validate
    validate -->|Invalid → Fatal exit| err["❌ Error + description"]
    validate --> json
    json --> go
    go --> app
```

Configuration is loaded by `internal/config/config.go` using the CUE Go SDK for validation and Go's `encoding/json` for deserialization.

---

## Structure Overview

```yaml
app:          # Application identity (name, env)
logger:       # Structured logging (level, format, sampling)
otel:         # OpenTelemetry tracing & metrics export
server:       # HTTP/gRPC server, TLS, timeouts, CORS
datastores:   # PostgreSQL DSN & pool; S3 endpoint, keys, SSE
policy:       # Object/upload constraints and allowed types
security:     # Tenant trust model, RLS, sensitive log masking
timeouts:     # Per-operation-category timeouts
rate_limit:   # Per-tenant token-bucket limits
cache:        # LRU metadata cache
idempotency:  # Safe-retry key TTL
housekeeping: # Background Reaper task intervals and TTLs
```

Default config file path: `configs/paladin.yaml`

---

## `app` — Application Identity

```yaml
app:
  name: paladin   # Used as logger default service field
  env: local                   # local | staging | prod
```

| Key | Default | Description |
|-----|---------|-------------|
| `name` | — | Service name, injected into logs and OTel resource |
| `env` | `local` | Deployment environment; constrainted to `local\|staging\|prod` |

---

## `logger` — Structured Logging

```yaml
logger:
  level: info          # debug | info | warn | error | dpanic | panic | fatal
  format: json         # json (production) | console (development)
  development: false   # DPanic → panic in development mode
  disable_caller: false
  disable_stacktrace: false
  sampling:
    enabled: true
    initial: 100       # First N logs per second passed through
    thereafter: 100    # Every Nth log after that
  fields:
    service: paladin  # Overrides default (app.name)
    env: local                     # Overrides default (app.env)
```

> Tip: set `format: console` and `level: debug` for local development.

---

## `otel` — OpenTelemetry

```yaml
otel:
  enabled: false
  endpoint: "otel-collector:4317"
  protocol: grpc        # grpc | http
  insecure: true        # Disable mTLS on the OTLP connection
  resource:
    service.name: paladin
    deployment.environment: local
```

When `enabled: true`, the service exports traces and metrics to the configured OTLP collector. All 15 service methods are instrumented with 100% coverage.

**Prometheus metrics** are always available at `GET /metrics` regardless of OTel status:

| Metric | Description |
|--------|-------------|
| `paladin_object_operation_duration_seconds` | Operation latency by method |
| `paladin_s3_operation_duration_seconds` | S3 SDK call latency |
| `paladin_db_query_duration_seconds` | Database query latency |
| `paladin_cache_operations_total` | Cache hits and misses |
| `paladin_rate_limiter_tenants` | Active per-tenant rate limiters |

---

## `server` — HTTP & gRPC

```yaml
server:
  mode: release        # debug | test | release (Gin mode)
  name: paladin

  http:
    addr: "0.0.0.0:8080"
    read_header_timeout: 5s
    read_timeout: 30s
    write_timeout: 30s
    idle_timeout: 90s
    max_header_bytes: 1048576   # 1 MiB
    max_body_bytes: 10485760    # 10 MiB
    request_id_header: "X-Request-Id"
    real_ip_header: "X-Forwarded-For"
    trusted_proxies: []         # CIDRs/IPs of trusted reverse proxies
    cors_allowed_origins: ["*"] # WARNING: restrict in staging/prod

    tls:
      enabled: false
      cert_path: ""
      key_path: ""
      ca_path: ""
      server_name: ""
      insecure_skip_verify: false

  grpc:
    addr: "0.0.0.0:9090"
    reflection_enabled: false
    max_recv_msg_size: 4194304  # 4 MiB
    max_send_msg_size: 4194304  # 4 MiB

  shutdown_timeout: 20s
  log_probes: false             # Log /health/* requests
```

---

## `datastores` — PostgreSQL & S3

### PostgreSQL

```yaml
datastores:
  postgres:
    dsn: "postgres://user:pass@host:5432/dbname?sslmode=disable"
    pool:
      max_conns: 20
      min_conns: 2
      max_conn_lifetime: 30m
      max_conn_idle_time: 5m
    timeouts:
      connect: 5s
      statement: 0s     # 0s = disabled; code checks for zero before enforcing
    healthcheck_period: 30s
```

For production: use `sslmode=require` and inject credentials via environment/secrets manager.

### S3-Compatible Storage

```yaml
datastores:
  s3:
    bucket: my-bucket
    region: us-east-1
    endpoint: "https://s3.amazonaws.com"       # or SeaweedFS/MinIO endpoint
    public_endpoint: "https://cdn.example.com" # Used for presigned URL generation
    force_path_style: true   # Required for MinIO, SeaweedFS; false for AWS
    access_key: "${S3_ACCESS_KEY}"
    secret_key: "${S3_SECRET_KEY}"
    presign_ttl: 15m
    part_size: "8MB"
    sse_type: "AES256"       # AES256 | aws:kms
    sse_key_id: ""           # Required when sse_type = aws:kms
```

> `public_endpoint` is the URL embedded in presigned URLs — it must be reachable by the end client (browser or service). It may differ from `endpoint` when the internal S3 address is not publicly routable.

---

## `policy` — Object Policies

```yaml
policy:
  max_object_size: "100MB"
  max_multipart_size: "1TB"
  min_part_size: "5MB"         # AWS S3 minimum; last part is exempt
  max_part_size: "5GB"
  max_parts: 10000
  presign_put_ttl: 15m
  presign_get_ttl: 15m
  presign_part_ttl: 15m
  allowed_content_types:       # Empty list = all types allowed
    - "image/jpeg"
    - "image/png"
    - "application/pdf"
  labels_max_bytes: 4096
  labels_max_keys: 10
  external_ref_max_len: 256
  object_key_max_len: 1024
```

> CUE validates `min_part_size ≥ 5MB` to prevent AWS S3 multipart failures.

---

## `security` — Tenant Isolation

```yaml
security:
  trust_tenant_id_from_request: true  # Accept X-Tenant-ID header
  reject_tenant_mismatch: true        # Fail if header ≠ auth context
  enable_rls: false                   # WARNING: requires migration 003
  log_sensitive: false                # Mask credentials in logs
```

```mermaid
flowchart TD
    req["Incoming Request"] --> hdr{"X-Tenant-ID\npresent?"}
    hdr -- No --> rej401["401 Unauthorized"]
    hdr -- Yes --> trust{"trust_tenant_id\n_from_request?"}
    trust -- true --> accept["Accept as tenant context"]
    trust -- false --> oidc["Derive from OIDC claims"]
    oidc --> match{"reject_tenant\n_mismatch?"}
    match -- true + mismatch --> rej401
    match -- false or match --> accept
    accept --> rls{"enable_rls?"}
    rls -- true --> setpg["SET LOCAL app.tenant_id"]
    rls -- false --> query["Scoped WHERE tenant_id = ?"]
    setpg --> query
```

---

## `timeouts` — Operation Timeouts

```yaml
timeouts:
  fast_operation: 5s      # GetObject, GetMeta, Delete, PatchMeta
  default_operation: 30s  # CreateObject, ListObjects
  s3_operation: 60s       # All S3 SDK calls
  long_operation: 2m      # CompleteMultipart
```

---

## `rate_limit` — Per-Tenant Rate Limits

```yaml
rate_limit:
  requests_per_second: 300
  burst: 500
  max_tenants: 10000
  cleanup_ttl: 10m
  cleanup_interval: 5m
```

Token-bucket algorithm. Exceeding the limit returns `429 Too Many Requests`. Inactive tenant buckets are automatically garbage-collected after `cleanup_ttl`.

---

## `cache` — Metadata Cache

```yaml
cache:
  enabled: true
  max_size: 1000   # Max entries in LRU cache
  ttl: 5m
```

The LRU cache stores object metadata to serve repeated reads without hitting PostgreSQL. Cache entries are invalidated on every write (create, update, delete). Expected hit rate: 70–90% for read-heavy workloads.

---

## `idempotency` — Safe Retries

```yaml
idempotency:
  enabled: true
  ttl: 24h
```

When a client sends `Idempotency-Key: <value>`, the response is cached for `ttl`. Repeated requests with the same key within the TTL return the original response without creating duplicates.

---

## `housekeeping` — Background Reaper

```yaml
housekeeping:
  enable_reaper: true
  pending_ttl: "24h"              # Abandoned pending objects → hard delete
  multipart_ttl: "72h"           # Stale multipart sessions → abort
  gc_interval: "1h"              # How often the Reaper runs
  delete_orphaned_parts: false   # Also delete S3 parts with no metadata
```

```mermaid
flowchart LR
    reaper["Reaper\n(every gc_interval)"] --> p["Query: pending\nwhere expires_at < now"]
    reaper --> m["Query: multipart\nwhere expires_at < now"]
    reaper --> a["Query: audit_logs\nwhere created_at < now - ttl"]
    p --> del1["Hard delete object\n+ S3 object"]
    m --> del2["Abort multipart\nin S3 + DB"]
    a --> del3["DELETE audit rows"]
```

> Set `delete_orphaned_parts: true` in production to reclaim S3 storage from aborted/expired multipart uploads.

---

## Secret Management

Sensitive fields should not be hardcoded in config files. Recommended patterns:

| Field | Recommended approach |
|-------|---------------------|
| `datastores.postgres.dsn` | Kubernetes Secret → env var `DATABASE_URL` |
| `datastores.s3.access_key` | Kubernetes Secret / IAM role (IRSA) |
| `datastores.s3.secret_key` | Kubernetes Secret / IAM role (IRSA) |
| `otel.endpoint` | ConfigMap override |

---

## Environment-Specific Files

Use separate YAML files per environment and override via `--config` flag or `CONFIG_PATH` env var:

```
configs/
├── paladin.yaml        # Local dev defaults
├── paladin.staging.yaml
└── paladin.prod.yaml
```

---

*Last updated: 2026-02-24 · Schema: internal/config/schema.cue*
