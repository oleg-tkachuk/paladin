# ADR-0001: OpenTelemetry observability baseline

- **Status:** Accepted (implemented 2026-06)
- **Context:** In production the only structured signal was access logs.
  Cross-plane debugging ("this presign took 1.4s — why?") and SLO/RED
  metrics (rate, errors, duration per RPC) had no source. The OTel SDK
  init (`internal/observability/otel.go` `InitOTel`: TracerProvider +
  MeterProvider + OTLP exporter + graceful shutdown) already existed and
  was wired into `app.go`, but nothing was instrumented.

## Decision

Instrument the two highest-value layers via well-trodden libraries,
keeping the install unconditional (no per-call config branch):

- **RPC layer:** `connectrpc.com/otelconnect` interceptor prepended to
  every plane chain (data / iam / admin). One span per RPC named from
  the procedure, wrapping auth + handler, plus `rpc.server.*` RED
  metrics on every call.
- **DB layer:** `github.com/exaring/otelpgx` query tracer on the pgx
  pool — one child span per query under the RPC span.

Both use the **global** Tracer/Meter providers, which are no-ops when
OTel is disabled (`InitOTel` not called), so the instrumentation costs
nothing when off — no `if cfg.OTel.Enabled` scattered through the code.

Endpoint / enable flow from `config.otel` (`endpoint`, `protocol`,
`insecure`); prod enables it against `otel-collector:4317`.

## Consequences

- Every RPC and query is traceable end-to-end once a collector is wired;
  RED metrics are emitted without per-handler code.
- **Follow-up (not in this baseline):** committed Grafana dashboards
  under `deploy/grafana/`, span attributes for tenant_id / object_key
  (careful with cardinality), and exemplars linking metrics→traces.
- Collector choice (Tempo / Honeycomb / Datadog) is an operator concern
  — PALADIN only speaks OTLP.
