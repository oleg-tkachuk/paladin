# Paladin Configuration Guide

## Table of Contents

- [Overview](#overview)
- [Configuration System](#configuration-system)
- [Configuration Structure](#configuration-structure)
- [Datastores (S3 & Postgres)](#datastores-s3--postgres)
- [Policies & Limits](#policies--limits)
- [Security & Isolation](#security--isolation)
- [Housekeeping (The Reaper)](#housekeeping-the-reaper)
- [Rate Limiting](#rate-limiting)
- [Performance (Cache & Timeouts)](#performance-cache--timeouts)
- [Environment Variables](#environment-variables)
- [Validation](#validation)

---

## Overview

The Paladin (PALADIN) uses a unified configuration system based on:

- **YAML** for configuration files
- **CUE** for schema validation and defaults
- **Go structs** for type-safe access

This ensures that the service is always started with a valid, secure, and well-defined configuration.

---

## Configuration System

### Architecture

```mermaid
graph LR
    A[YAML File] --> B[CUE Validation]
    B --> C[Go Struct]
    C --> D[Application]
```

Detailed configuration logic resides in `internal/config/`, with the schema defined in `schema.cue`.

---

## Configuration Structure

### Top-Level Sections

```yaml
app:          # Application metadata
logger:       # Logging configuration
server:       # HTTP/gRPC server settings
datastores:   # S3 and PostgreSQL connections
policy:       # Global object policies
security:     # Multi-tenancy and RLS
housekeeping: # Background cleanup tasks
rate_limit:   # Per-tenant request limits
cache:        # Metadata caching
timeouts:     # Operation-specific timeouts
idempotency:  # Request idempotency settings
otel:         # OpenTelemetry configuration
policy:       # Global object policies
security:     # Multi-tenancy and RLS
housekeeping: # Background cleanup tasks
rate_limit:   # Per-tenant request limits
cache:        # Metadata caching
timeouts:     # Operation-specific timeouts
idempotency:  # Request idempotency settings
otel:         # OpenTelemetry configuration
```

---

## Datastores (S3 & Postgres)

### S3 Storage

The primary storage backend for object data.

```yaml
datastores:
  s3:
    bucket: "my-bucket"
    region: "us-east-1"
    endpoint: "s3.amazonaws.com"
    public_endpoint: "" # Optional: URL for public access
    force_path_style: true # Set true for MinIO/LocalStack
    access_key: "${S3_ACCESS_KEY}"
    secret_key: "${S3_SECRET_KEY}"
    presign_ttl: "15m"
    part_size: "8MB"
    sse_type: "AES256" # AES256 or aws:kms
    sse_key_id: "" # Required for aws:kms
```

### PostgreSQL

Stores object metadata and multipart sessions.

```yaml
datastores:
  postgres:
    dsn: "${DATABASE_URL}"
    pool:
      max_conns: 20
      min_conns: 2
      max_conn_lifetime: 30m
      max_conn_idle_time: 5m
```

---

## Policies & Limits

Defines the constraints for objects and multipart uploads.

```yaml
policy:
  max_object_size: "100MB"
  max_multipart_size: "1TB"
  min_part_size: "5MB"
  max_part_size: "5GB"
  max_parts: 10000
  presign_put_ttl: "15m"
  presign_get_ttl: "15m"
  presign_part_ttl: "15m"
  allowed_content_types: [] # Empty = all allowed
  labels_max_bytes: 4096
  labels_max_keys: 10
  external_ref_max_len: 256
```

---

## Security & Isolation

Controls how the service handles tenant identity and database security.

```yaml
security:
  trust_tenant_id_from_request: true # Trust X-Tenant-ID header
  reject_tenant_mismatch: true # Reject if header doesn't match auth context
  enable_rls: false # Enable PostgreSQL Row Level Security
  log_sensitive: false # Avoid logging PII/secrets
```

---

## Housekeeping (The Reaper)

Manages background cleanup of incomplete uploads and orphaned parts.

```yaml
housekeeping:
  enable_reaper: true
  pending_ttl: "24h" # Clean up pending objects after 24h
  multipart_ttl: "72h" # Clean up multipart sessions after 72h
  gc_interval: "1h" # Run cleanup every hour
  delete_orphaned_parts: false # Delete files from S3 without metadata
```

---

## Rate Limiting

Per-tenant rate limiting to prevent noisy neighbors.

```yaml
rate_limit:
  requests_per_second: 100
  burst: 100
  max_tenants: 10000
  cleanup_ttl: "10m"
  cleanup_interval: "5m"
```

---

## Performance (Cache & Timeouts)

### Metadata Cache

Caches object metadata to reduce database load.

```yaml
cache:
  enabled: true
  max_size: 1000 # Number of entries
  ttl: "5m"
```

### Timeouts

```yaml
timeouts:
  fast_operation: "5s"    # Metadata lookups
  default_operation: "30s" # Standard API calls
  s3_operation: "60s"     # AWS SDK calls
  long_operation: "2m"     # Batch / Multi-part operations
```

---

## Environment Variables

Use `${VAR_NAME}` syntax for secret substitution:

- `DATABASE_URL`
- `S3_ACCESS_KEY`
- `S3_SECRET_KEY`
- `OTEL_EXPORTER_OTLP_ENDPOINT`

---

## Validation

The service performs schema validation on startup. If configuration is invalid, the service will exit with a descriptive error:

```text
configuration validation failed: policy.min_part_size: invalid value "1MB" (minimum 5MB required by S3)
```

---

**Last Updated:** 2026-02-12  
**Version:** 1.0
