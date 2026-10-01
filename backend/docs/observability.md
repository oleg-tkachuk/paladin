# Observability

Traces, metrics, logs and health endpoints, as the backend emits them. The
Grafana dashboards and alerts that read them are in
[deploy/grafana](../../deploy/grafana/README.md).

## Traces

OpenTelemetry, exported over OTLP. Every Connect RPC gets a server span
(`otelconnect`), and the W3C `traceparent` header propagates across planes.

| Key | Default | Meaning |
|---|---|---|
| `otel.enabled` | `false` | turn on once a collector is reachable |
| `otel.endpoint` | `otel-collector:4317` | collector `host:port` (gRPC) or URL (HTTP) |
| `otel.protocol` | `http` | `grpc` or `http` |
| `otel.insecure` | `true` | plaintext to the collector |

## Metrics

`otel.metrics_exporter` picks how metrics leave the process: `otlp` pushes
them with the traces; `prometheus` serves them for a scrape on
`otel.metrics_addr` (`:9095` in the chart), a plain-HTTP listener of its own
so a scraper does not need to trust the planes' internal CA.

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
