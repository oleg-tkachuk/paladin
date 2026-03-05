# Telemetry

**Source:** `internal/metrics/metrics.go`, `internal/metrics/otel.go`, `internal/observability/`, `internal/api/http/router.go`

## Telemetry Stack

| Component | Technology |
|---|---|
| Metrics (Prometheus) | `github.com/prometheus/client_golang` (`promauto`) |
| Tracing & Metrics (OTel) | `go.opentelemetry.io/otel` |
| OTLP Exporter | gRPC or HTTP to configurable collector endpoint |
| Scrape Endpoint | `GET /metrics` (Prometheus text format) |

## Prometheus Metrics Catalog

All metrics are registered at package initialization via `promauto`. Metric names are prefixed with `paladin_`.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `paladin_object_operation_duration_seconds` | Histogram | `operation`, `status` | Duration of object service operations |
| `paladin_s3_operation_duration_seconds` | Histogram | `operation`, `status` | Duration of S3 client operations |
| `paladin_db_query_duration_seconds` | Histogram | `query`, `status` | Duration of PostgreSQL queries |
| `paladin_db_connections` | Gauge | `state` (`acquired`, `idle`, `max`, `total`) | DB connection pool state |
| `paladin_cache_operations_total` | Counter | `operation` (`get`/`set`/`delete`), `result` (`hit`/`miss`/`success`/`error`) | Cache operation counters |
| `paladin_rate_limiter_tenants` | Gauge | `state` (`active`, `max`) | Active per-tenant rate limiter slots |
| `paladin_http_requests_total` | Counter | `method`, `path`, `status` | Total HTTP requests by method/path/status |
| `paladin_http_request_duration_seconds` | Histogram | `method`, `path`, `status` | HTTP request latency distribution |
| `paladin_objects_total` | Gauge | `status` | Total objects by lifecycle status |
| `paladin_multipart_uploads_total` | Gauge | `status` | Total multipart uploads by status |

### Histogram Bucket Boundaries

| Metric | Buckets (seconds) |
|---|---|
| `paladin_object_operation_duration_seconds` | `.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10` |
| `paladin_s3_operation_duration_seconds` | `.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30` |
| `paladin_db_query_duration_seconds` | `.001, .005, .01, .025, .05, .1, .25, .5, 1` |
| `paladin_http_request_duration_seconds` | `.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5` |

### Cardinality Notes

- `paladin_http_requests_total` and `paladin_http_request_duration_seconds` use `path` as a label. High-cardinality dynamic paths (e.g. `/v1/objects/:id`) should be normalized to route templates in production to avoid metric explosion.
- `paladin_db_query_duration_seconds` `query` label should be a named query identifier, not raw SQL.

## OpenTelemetry

When `otel.enabled: true`:

### Instruments

All OTel instruments are registered in `internal/metrics/otel.go` under meter name `"github.com/oleg-tkachuk/paladin"`:

| Instrument | Type | Unit | Description |
|---|---|---|---|
| `paladin_object_operation_duration_seconds` | Float64Histogram | `s` | Object operation durations |
| `paladin_object_operations_total` | Int64Counter | — | Object operation count |
| `paladin_s3_operation_duration_seconds` | Float64Histogram | `s` | S3 operation durations |
| `paladin_s3_operations_total` | Int64Counter | — | S3 operation count |
| `paladin_db_query_duration_seconds` | Float64Histogram | `s` | DB query durations |
| `paladin_db_queries_total` | Int64Counter | — | DB query count |
| `paladin_cache_operations_total` | Int64Counter | — | Cache operation count |

### Tracing

HTTP tracing middleware is enabled via `middleware.OTelHTTP()` when `otel.enabled: true` (applied in the HTTP middleware stack as step 10).

- Span attributes include: HTTP method, URL path, response status code.
- Trace context is propagated via standard W3C Trace-Context headers (`traceparent`, `tracestate`).

### OTLP Exporter

- **Protocol:** `grpc` or `http` (from `otel.protocol`)
- **Endpoint:** `otel.endpoint` (default: `otel-collector:4317`)
- **Insecure:** `otel.insecure: true` skips TLS (for local/dev)
- **Resource attributes:** `service.name`, `deployment.environment`

## Health Probes

Health probes are registered directly on the Gin router (not behind auth or rate limiting).

| Endpoint | Type | Description |
|---|---|---|
| `GET /health/livez` | Liveness | Always returns `200 OK` after process start |
| `GET /health/readyz` | Readiness | Checks PostgreSQL ping + S3 HeadBucket; returns `503` if any dependency is unhealthy |
| `GET /health/startupz` | Startup | Returns `503` until `atomic.Bool` is set by `app.Run()` after initialization |

### Readiness Check Details

The `HealthService.CheckReady()` method (`internal/service/health.go`) performs:

1. PostgreSQL `Ping()` — latency measured and returned as `postgresql.latency_ms`
2. S3 `HeadBucket()` — latency measured and returned as `seaweedfs.latency_ms`
3. Circuit breaker state for each breaker — reported as `breakers` map
4. Connection pool stats — reported as `pool_stats`

Response body includes detailed dependency status suitable for monitoring dashboards.
