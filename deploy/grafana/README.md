# Grafana dashboards and alerts

Committed dashboards and Prometheus alerting rules for the OpenTelemetry
baseline (ADR-0001). Import a dashboard's JSON via Grafana → Dashboards →
Import and pick your Prometheus datasource when prompted; load a rule file
through `rule_files:` or as the `spec.groups` of a `PrometheusRule`.

| File | Contents |
|------|----------|
| `paladin-rpc-red.json` | RPC rate, errors and duration per service and method |
| `paladin-operations.json` | outbox, workers, Postgres, rate limiting, presigns, capability charges |
| `worker-alerts.yaml` | a worker that stopped ticking, or fails every tick |
| `paladin-alerts.yaml` | outbox not draining, rate limiting failing open, refused capability charges, throttled tenants |

`task -t Taskfile.dev.yaml verify:grafana-rules` parses every rule file and
dashboard query with `promtool`, and runs the alert unit tests in
`*-alerts.test.yaml`. Metric names are the Prometheus rendering of the
instruments in the code (`paladin.outbox.pending` →
`paladin_outbox_pending`); all of them exist only with `otel.enabled: true`.

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
(`otel.enabled: true`, `otel.endpoint: otel-collector:4317`) — see ADR-0001.

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

## `worker-alerts.yaml` — background-worker alerts

Prometheus alerting rules for the periodic workers (reconciler, purgers,
reapers, lifecycle, replication). Two rules, each fanned out per `worker`
label so there is no per-worker threshold to maintain:

- **`PaladinWorkerStalled`** — no completed tick in >5× the worker's own
  `paladin_worker_interval_seconds`.
- **`PaladinWorkerTicksAllFailing`** — only `outcome="error"` and zero successes
  over 15m.

Signal: the `paladin_worker_*` instruments emitted by `internal/worker.RunTicker`
(tick count + outcome, duration, last-run timestamp, interval). Same caveat as the
dashboard — only live when `otel.enabled: true`. Drop the file
into Prometheus `rule_files:` or wrap it in a `PrometheusRule` CR. Response
procedure: [`docs/runbooks/worker-stalled.md`](../../docs/runbooks/worker-stalled.md).

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

`tenant_id` appears on the rate-limit and capability-charge series only; the
outbox gauge carries the deepest tenant's depth, not a per-tenant series.

## `paladin-alerts.yaml` — outbox, rate limiting, charges

| Alert | Fires when | Severity |
|-------|-----------|----------|
| `PaladinOutboxNotDraining` | the minimum outbox depth over 15m is above 500, for 10m | critical |
| `PaladinRateLimitFailingOpen` | either limiter admitted a request because its storage failed, in the last 10m | critical |
| `PaladinCapabilityChargesRefused` | over 90% of a tenant's charges were refused for 15m | warning |
| `PaladinTenantThrottled` | the per-tenant limiter refused over 1 req/s for 10m | warning |

The counters behind the last three exist only after their path has run once.
The fail-open rule treats an absent counter as zero; none of the rules alert
on absence.
