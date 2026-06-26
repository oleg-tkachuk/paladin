# Grafana dashboards

Committed dashboards for the OpenTelemetry baseline (ADR-0001). Import the
JSON via Grafana → Dashboards → Import, and pick your Prometheus datasource
when prompted.

## `paladin-rpc-red.json` — RPC RED

Rate / Errors / Duration for every PALADIN Connect RPC across all three planes
(data / iam / admin). The signal comes from the `otelconnect` interceptor's
`rpc.server.*` instruments, which only emit once OTel is **enabled**
(`otel.enabled: true`, `otel.endpoint: otel-collector:4317`) — see ADR-0001.

### Assumptions (adjust if your pipeline differs)

- **Datasource:** Prometheus scraping the OTLP collector's Prometheus
  exporter. PALADIN itself only speaks OTLP (push); the collector is the
  metrics→Prometheus bridge.
- **Metric name:** `rpc_server_duration_milliseconds_{bucket,sum,count}`.
  This is the Prometheus rendering of OTel `rpc.server.duration` (unit `ms`,
  emitted by otelconnect v0.9.0). If your collector emits seconds
  (`rpc_server_duration_seconds_*`) or applies a different naming
  convention, update the panel expressions accordingly.
- **Labels:** `rpc_service`, `rpc_method`, and `rpc_connect_rpc_error_code`
  (set on errored Connect calls). gRPC-protocol callers surface
  `rpc_grpc_status_code` instead — swap the error-rate filter if you front
  the planes with gRPC.

### Note on per-tenant filtering

`tenant_id` / `tenant_slug` are attached to **traces** (the RPC span), not to
these metrics — adding them as metric labels would blow up cardinality. Use
the trace view (filter spans by `paladin.tenant_id`) for per-tenant drill-down;
metrics stay aggregated by service/method. Exemplars (SDK-default,
trace-based) link a histogram bucket back to a sampled trace when the
backend supports them.
