# Telemetry - Paladin (PALADIN)

## Overview

PALADIN is instrumented with [OpenTelemetry](https://opentelemetry.io/) for distributed tracing and exports metrics for [Prometheus](https://prometheus.io/).

## 1. Metrics

Accessible via the `/metrics` endpoint if enabled.

### Key Metrics

- `paladin_http_requests_total`: Counter of HTTP requests (labels: `method`, `path`, `status`).
- `paladin_http_request_duration_seconds`: Histogram of HTTP request latency.
- `paladin_db_queries_total`: Counter of database queries (labels: `op`, `status`).
- `paladin_s3_ops_total`: Counter of S3 operations (labels: `op`, `status`).
- `paladin_objects_created_total`: Counter of new objects registered.
- `paladin_storage_usage_bytes`: Gauge of total storage used (per tenant).

## 2. Tracing

Tracing is automatically propagated across HTTP/Connect boundaries using the `traceparent` header.

### Configuration

- `otel.enabled`: Boolean to enable/disable tracing.
- `otel.endpoint`: OTLP collector endpoint (e.g., `otel-collector:4317`).
- `otel.protocol`: `grpc` or `http/protobuf`.

## 3. Health Checks

Health information is available at `/health/livez`, `/health/readyz`, and `/health/startupz`.

- **Readiness Check**: Performs a `Ping` to Postgres and a `HEAD` request to a test object in S3.
- **Dependency Map**: The `readyz` endpoint returns a detailed JSON map of all downstream service statuses.

### Example Health Response

```json
{
  "status": "ready",
  "dependencies": {
    "postgresql": { "status": "ok", "latency_ms": 2 },
    "seaweedfs": { "status": "ok", "latency_ms": 15 },
    "breakers": {}
  }
}
```
