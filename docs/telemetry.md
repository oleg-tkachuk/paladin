# Telemetry

The `paladin` utilizes a dual-stack configuration for observability: **Prometheus** for local/pull-based metrics and **OpenTelemetry (OTel)** for distributed tracing and OTLP metric exports.

## 1. Stack Components

- **Metrics Library:** `github.com/prometheus/client_golang` and `go.opentelemetry.io/otel/metric`
- **Tracing Library:** `go.opentelemetry.io/otel/trace`
- **Middleware:** Web frameworks and gRPC servers are instrumented via `otelhttp` and `otelgrpc`.
- **Exporters:** Metrics and Traces pushed via OTLP (`otlpmetricgrpc`, `otlptracegrpc`) to an OpenTelemetry collector.

## 2. Health & Readiness Probes

Exposed unauthenticated to orchestrators (e.g., Kubernetes):

- `GET /health/livez`: Basic liveness check. Asserts the server is accepting connections.
- `GET /health/readyz`: Strict readiness check. Verifies the Postgres connection pool is healthy and the S3 backend responds to `HEAD Bucket` (`pingS3`).
- `GET /health/startupz`: Used during the boot phase to grant the service time to warm up connections.

The health endpoint payloads return a `status` top-level struct, including `dependencies` statuses detailing component latencies (`postgresql`, `seaweedfs`).

## 3. Metrics Catalog

The service defines custom application metrics (`internal/metrics/metrics.go` and `otel.go`).

| Metric Name | Type | Labels | Description |
|---|---|---|---|
| `paladin_object_operation_duration_seconds` | Histogram | `operation`, `status` | Latency and count of logical object operations |
| `paladin_s3_operation_duration_seconds` | Histogram | `operation`, `status` | Latency and count of external S3 API calls |
| `paladin_db_query_duration_seconds` | Histogram | `query`, `status` | Latency of internal database interactions |
| `paladin_db_connections` | Gauge | `state` (`acquired`, `idle`, `max`, `total`) | Connection pool size monitoring |
| `paladin_cache_operations_total` | Counter | `operation`, `result` (`hit`/`miss`) | Track internal cache effectiveness |
| `paladin_rate_limiter_tenants` | Gauge | `state` | Track active vs max concurrent rate limiters |
| `paladin_http_requests_total` | Counter | `method`, `path`, `status` | Raw HTTP inbound volume |
| `paladin_http_request_duration_seconds` | Histogram | `method`, `path`, `status` | HTTP endpoint tail latencies |
| `paladin_objects_total` | Gauge | `status` | Overall point-in-time counts of tracked metadata rows |
| `paladin_multipart_uploads_total` | Gauge | `status` | Active/pending multipart upload workflows |

*Note: High cardinality fields like `tenant_id` are intentionally excluded from Prometheus labels to prevent TSDB explosion unless explicitly opted into via configuration.*

## 4. Distributed Tracing

Tracing is initialized in `internal/observability/otel.go`.

**Span Structure:**

- Every HTTP/gRPC ingress creates an implicit root span (or continues a propagated trace).
- `SpanContext` is pushed into the `context.Context` payload.
- W3C Trace Context standards (`traceparent`, `tracestate`) are parsed from inbound HTTP headers to maintain linkage across microservice boundaries.
- The logger automatically extracts and prints the `trace_id` for logs emitted within the span.
