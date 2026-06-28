# ADR-0001: OpenTelemetry observability baseline

- **Status:** Accepted (implemented 2026-06; activated + follow-ups
  2026-06-26)
- **Context:** In production the only structured signal was access logs.
  Cross-plane debugging ("this presign took 1.4s — why?") and SLO/RED
  metrics (rate, errors, duration per RPC) had no source. The OTel SDK
  init (`internal/observability/otel.go` `InitOTel`: TracerProvider +
  MeterProvider + OTLP exporter + graceful shutdown) already existed and
  was wired into `app.go`, but nothing was instrumented.
  - **2026-06-26 correction:** the instrumentation (otelconnect + otelpgx)
    was wired, but `InitOTel` had **no caller** — `boot()` never invoked
    it and every container got a `nil` shutdown hook. So the global
    providers stayed no-ops and the baseline emitted nothing in prod. Now
    fixed (see Consequences).

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
- **Activation (2026-06-26):** `boot()` now calls `InitOTel(ctx, cfg.OTel)`
  once per process (the single point every `serve`/`migrate`/`bootstrap`
  subcommand funnels through) and defers the returned shutdown via
  `flushOTel` so the final batch flushes on SIGTERM. Disabled config →
  no-op shutdown, zero cost; an init error is logged, not fatal.
- **Span attributes (2026-06-26):** the auth interceptor stamps
  `paladin.tenant_id` (+ `paladin.tenant_slug` when present) on the active RPC
  span for every authenticated call — bounded cardinality, one chokepoint.
  `object_key` is intentionally **not** a span attribute: it is per-request
  and unbounded, so it stays a handler-local concern, not a blanket
  attribute.
- **Dashboards (2026-06-26):** `deploy/grafana/paladin-rpc-red.json` — a RED
  dashboard over the `rpc.server.*` metrics, with `deploy/grafana/README.md`
  documenting the datasource + metric-name assumptions.
- **Log↔trace correlation (2026-06-28):** `logger.enrich` already stamps
  `trace_id` / `span_id` / `request_id` / `tenant_id` on every structured log
  line. `deploy/grafana/datasources.example.yaml` is the operator-facing glue
  that makes it navigable: a Loki derived field links log → trace, Tempo
  `tracesToLogsV2` links trace → log, and Prometheus exemplars link metric →
  trace. Backend-agnostic (Tempo/Loki/Prometheus reference set).
- **Exemplars:** handled by the SDK default (trace-based exemplar filter)
  once OTel is active — measurements taken inside a sampled span carry a
  trace exemplar over OTLP. Rendering them (Tempo/Prometheus exemplars) is
  an operator/backend concern; no extra PALADIN code is required.
- Collector choice (Tempo / Honeycomb / Datadog) is an operator concern
  — PALADIN only speaks OTLP.
