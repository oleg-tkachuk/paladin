# Grafana dashboards

Committed dashboards for the OpenTelemetry baseline (ADR-0001). Import a
dashboard's JSON via Grafana → Dashboards → Import and pick your Prometheus
datasource when prompted.

The alerting rules ship in the backend chart's PrometheusRule
(`metrics.alerts`, ADR-0023), not here. Without Prometheus Operator, render
the chart and take `spec.groups` from it as a rule file:

    helm template paladin-core backend/deploy/chart -f <your values> \
      --set metrics.alerts.enabled=true \
      --show-only templates/prometheusrule.yaml | yq '.spec'

| File | Contents |
|------|----------|
| `paladin-rpc-red.json` | RPC rate, errors and duration per service and method |
| `paladin-operations.json` | outbox, workers, Postgres, rate limiting, presigns, capability charges, overdue uploads |

`task -t Taskfile.dev.yaml verify:grafana-dashboards` parses every dashboard
query with `promtool`; `verify:chart-alerts` checks the rules. Metric names
are the Prometheus rendering of the instruments in the code
(`paladin.outbox.pending` → `paladin_outbox_pending`); all of them exist only
with `otel.enabled: true`.

## Datasource provisioning + log↔trace correlation

[`datasources.example.yaml`](datasources.example.yaml) is a reference Grafana
datasource provisioning file that wires the three Paladin signals together so a
single click pivots between them:

- **Log → trace.** Paladin stamps `trace_id` / `span_id` / `request_id` /
  `tenant_id` onto every log line written while serving an RPC
  (`middleware.LogContextStreaming`). The Loki datasource's *derived field* extracts `trace_id`
  and turns it into a "View trace" link into Tempo.
- **Trace → log.** Tempo's `tracesToLogsV2` jumps from a span back to the log
  lines sharing its `trace_id`.
- **Metric → trace.** Prometheus exemplars (SDK-default, trace-based) link a
  RED histogram bucket to the sampled trace behind an outlier latency.

It's an *example* — Paladin is backend-agnostic: traces go out over OTLP, and
metrics over OTLP or a Prometheus scrape (`otel.metrics_exporter`).
Tempo / Loki / Prometheus are the OSS reference set; swap the types/uids for
Honeycomb / Datadog / etc. The collector that bridges OTLP → these backends
is an operator concern (see ADR-0001).

## `paladin-rpc-red.json` — RPC RED

Rate / Errors / Duration for every Paladin Connect RPC across all three planes
(data / iam / admin). The signal comes from the `otelconnect` interceptor's
`rpc.server.call.duration` histogram, which only emit once OTel is **enabled**
(`otel.enabled: true`, with `otel.endpoint` naming the collector) — see ADR-0001.

### Assumptions (adjust if your pipeline differs)

- **Datasource:** Prometheus, holding either the collector's export of the
  OTLP metrics or a direct scrape of each role's `otel.metrics_addr`
  (`otel.metrics_exporter: prometheus`).
- **Metric name:** `rpc_server_call_duration_seconds_{bucket,sum,count}`, the
  Prometheus rendering of OTel `rpc.server.call.duration` (unit `s`,
  semconv 1.43, emitted by otelconnect v0.10.0).
- **Labels:** `rpc_method` in its fully qualified form
  (`paladin.data.v1.ObjectService/GetObject`), `rpc_system_name`, and
  `error_type` on failed calls. There is no `rpc_service` label; the panels
  derive the service from `rpc_method` with `label_replace`, and the
  `$service` variable is the part of `rpc_method` before the `/`.

### Note on per-tenant filtering

`tenant_id` / `tenant_slug` are attached to **traces** (the RPC span), not to
these metrics — adding them as metric labels would blow up cardinality. Use
the trace view (filter spans by `paladin.tenant_id`) for per-tenant drill-down;
metrics stay aggregated by service/method. Exemplars (SDK-default,
trace-based) link a histogram bucket back to a sampled trace when the
backend supports them.

## `paladin-operations.json` — Operations

The work that does not show up in RPC metrics.

| Panel | Metric | Source |
|-------|--------|--------|
| Outbox backlog, Outbox depth | `paladin_outbox_pending`, `paladin_outbox_pending_max_per_tenant` | `internal/worker/metrics.go` |
| Stalled workers, Worker ticks, Worker tick duration | `paladin_worker_*` | `internal/worker.RunTicker` |
| Postgres errors, Postgres operation latency | `db_client_operation_errors_total`, `db_client_operation_duration_seconds` (label `pgx_operation_type`) | otelpgx |
| Rate limiting failing open, Per-tenant rate-limit decisions | `paladin_tenant_ratelimit_*`, `paladin_api_token_ratelimit_fail_open_total` | `internal/middleware/tenant_ratelimit.go`, `internal/auth/api_token_metrics.go` |
| Presigns, Presign latency | `paladin_presign_total`, `paladin_presign_duration_seconds` (labels `op`, `outcome`) | `internal/metrics/domain.go` |
| Capability charges | `paladin_capability_charges_total` (labels `outcome`, `tenant_id`) | `internal/metrics/domain.go` |
| Overdue uploads | `paladin_objects_pending_overdue`, `paladin_objects_pending_overdue_age_seconds` | `internal/worker/metrics.go`, sampled by the reconciler |

`tenant_id` appears on the rate-limit and capability-charge series only; the
outbox gauge carries the deepest tenant's depth, not a per-tenant series.
