# Observability

Traces, metrics, logs and health endpoints, as the backend emits them. The
Grafana dashboards and alerts that read them are in
[deploy/grafana](../../deploy/grafana/README.md).

## Traces

OpenTelemetry, exported over OTLP. Every Connect RPC gets a server span
(`otelconnect`), and the W3C `traceparent` header propagates across planes.

| Key | Default | Meaning |
|---|---|---|
| `otel.enabled` | `false` | turn on once a collector is reachable; while off, no traces or metrics leave the process |
| `otel.endpoint` | `""` | collector `host:port` (gRPC) or URL (HTTP) |
| `otel.protocol` | `http` | `grpc` or `http` |
| `otel.insecure` | `true` | plaintext to the collector |

Defaults are those of `internal/config/schema.cue`. The chart's `values.yaml`
sets `otel.endpoint: otel-collector:4318` (the OTLP/HTTP port) and
`otel.insecure: false`.

## Metrics

`otel.metrics_exporter` picks how metrics leave the process: `otlp` (the
default) pushes them with the traces; `none` sends traces only; `prometheus`
serves them for a scrape. With `prometheus` every role except mcp serves
`/metrics` on `otel.metrics_addr` (default `0.0.0.0:9095`), a plain-HTTP
listener of its own so a scraper does not need to trust the planes' internal
CA, and on no other port (ADR-0023). mcp holds no instruments of its own and
serves none.

Under the Helm chart neither key is set directly: `metrics.mode`
(`push` | `scrape` | `off`) and `metrics.port` render them, together with the
container port, the scrape discovery (`metrics.discovery`: a PodMonitor or
`prometheus.io/*` annotations) and the NetworkPolicy rule, and the chart
refuses `config.otel.metrics_exporter` and `metrics_addr` so the two cannot
disagree. The alerting rules over these series ship in the chart's
PrometheusRule (`metrics.alerts`). See [docs/install.md](../../docs/install.md#metrics-and-alerts).

Instruments defined in `internal/metrics`:

| Metric | Kind | What it counts |
|---|---|---|
| `paladin_presign_total` | counter | presigned URLs issued, by operation and outcome |
| `paladin_presign_duration_seconds` | histogram | time to issue a presigned URL |
| `paladin_storage_calls_total` | counter | S3 calls the planes make, by operation and outcome |
| `paladin_storage_call_duration_seconds` | histogram | S3 call latency |
| `paladin_quota_decisions_total` | counter | quota checks, by scope and outcome |
| `paladin_capability_charges_total` | counter | capability charges, by outcome |
| `paladin_capability_charge_amount` | histogram | amount charged per call |
| `paladin_login_attempts_total` | counter | sign-ins, by method and outcome |
| `paladin_idempotency_lookups_total` | counter | idempotency-key lookups, by result |
| `paladin_object_lock_operations_total` | counter | object-lock changes |
| `paladin_resource_name_shape_total` | counter | collection names resolved, by shape ([canonical-resource-names.md](canonical-resource-names.md)) |
| `paladin_worker_runs_total` | counter | background job runs, by job and outcome |
| `paladin_worker_run_duration_seconds` | histogram | background job duration |
| `paladin_worker_last_run_timestamp_seconds` | gauge | last run per job |
| `paladin_worker_interval_seconds` | gauge | configured interval per job |

Instruments defined beside the code they measure, by their OTel name (the
Prometheus rendering replaces dots with underscores and adds unit and
`_total` suffixes, e.g. `paladin.outbox.pending` → `paladin_outbox_pending`):

| Metric | Kind | What it counts | Defined in |
|---|---|---|---|
| `paladin_object_transitions` | counter | object state transitions, by source signal | `internal/statemachine/metrics.go` |
| `paladin.outbox.pending` | gauge | `event_deliveries` rows pending, cluster-wide | `internal/worker/metrics.go` |
| `paladin.outbox.pending.max_per_tenant` | gauge | the deepest single tenant's pending backlog | `internal/worker/metrics.go` |
| `paladin.objects.pending_overdue` | gauge | `PENDING` objects past their presign expiry plus the reconciler's grace | `internal/worker/metrics.go` |
| `paladin.objects.pending_overdue.age` | gauge | how far past that deadline the oldest one is, in seconds | `internal/worker/metrics.go` |
| `paladin.tenant.ratelimit.decisions` | counter | per-tenant rate-limit decisions | `internal/middleware/tenant_ratelimit.go` |
| `paladin.tenant.ratelimit.fail_open` | counter | requests admitted because the bucket store failed | `internal/middleware/tenant_ratelimit.go` |
| `paladin.api_token.verify.duration_ms` | histogram | API-token verify latency | `internal/auth/api_token_metrics.go` |
| `paladin.api_token.ratelimit.decisions` | counter | per-token rate-limit decisions | `internal/auth/api_token_metrics.go` |
| `paladin.api_token.ratelimit.weighted_ratio` | histogram | weighted window count over capacity; above 1 is denied | `internal/auth/api_token_metrics.go` |
| `paladin.api_token.ratelimit.fail_open` | counter | requests admitted because the limiter failed | `internal/auth/api_token_metrics.go` |
| `paladin.db.replica.in_sync` | gauge | 1 while lag-tolerant reads go to the read replica | `internal/store/postgres/replica.go` |
| `paladin.db.replica.lag` | gauge | replica replay lag at the last probe, in seconds | `internal/store/postgres/replica.go` |
| `paladin.db.replica.reads` | counter | lag-tolerant reads, by `served_by` and `reason` | `internal/store/postgres/replica.go` |

RPC latency and counts come from `otelconnect`'s standard `rpc.server.*`
instruments.

## Logs

Structured JSON through [zap](https://github.com/uber-go/zap).

| Key | Meaning |
|---|---|
| `logger.level` | `debug`, `info`, `warn`, `error` |
| `logger.format` | `json` or `console` |
| `logger.development` | development mode: console-friendly output, stack traces on warn |
| `logger.disable_caller`, `logger.disable_stacktrace` | drop those fields |
| `logger.sampling.{enabled,initial,thereafter}` | rate-limit identical lines per second |
| `logger.fields.{service,env}` | default from `app.name` / `app.env` |

Every line carries `service` and `env`. Lines written while serving an RPC
also carry `trace_id`, `span_id`, `request_id` and `tenant_id`
(`middleware.LogContextStreaming`). A failed RPC — including an authentication
refusal — is logged once with `rpc`, `code`, `took_ms` and `request_id`
(`middleware.LogOutcome`); successful calls are left to tracing.

The caller of an RPC that fails on the server's side — `internal`, `unknown`,
`data_loss` — gets the code and `internal error; request id <id>`, never the
error itself (`middleware.ScrubInternal`, outermost on every plane). The log
line above, and the span, keep the original; the id is the caller's
`X-Request-Id`, or one minted for the request and returned in that header.
Find the cause by that id.

### `TLS handshake error ... client sent an HTTP request to an HTTPS server`

One line per plain-HTTP request made to a listener serving TLS; the address
is the client's. From a cluster address it is a misconfigured peer. From
`127.0.0.1` it is almost always `kubectl port-forward`: forwarded connections
reach the pod from loopback, so a `curl http://localhost:<port>` against a
forward to a TLS listener logs exactly this, and the client sees a bare 400.
Use `https://`, or the plain metrics or ops listener. A connection that opens
and sends nothing logs `EOF` instead.

## Health

Every role serves, on its own listener:

| Path | Purpose |
|---|---|
| `/livez` | the process is alive |
| `/readyz` | ready checks pass (database ping, S3 reachability, where the role uses them); 503 while shutting down |
| `/startupz` | startup checks pass |
| `/system/health.json` | a snapshot of every check, gated by `runtime.health_snapshot_token`; the console's health page reads it |

Probe paths per role are in `deployments.<role>` in the chart's `values.yaml`.
`runtime.log_probes: false` keeps probe hits out of the logs.
