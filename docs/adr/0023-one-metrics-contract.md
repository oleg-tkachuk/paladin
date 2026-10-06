# ADR-0023: One metrics contract from process to alert

- **Status:** Proposed 2026-10-06.

- **Context.** ADR-0001 settled what Paladin measures: otelconnect's RPC
  histogram, otelpgx, and the `paladin_*` instruments. That layer works. What
  breaks is the path from the process to a firing alert, which grew one role
  and one switch at a time:

  - **Three ways to serve `/metrics`.** With
    `config.otel.metrics_exporter: prometheus`, api and admin open a dedicated
    plain-HTTP listener on `otel.metrics_addr` (`:9095`). worker and
    dispatcher mount `/metrics` on their ops listener (`:8090`, `:8099`).
    ingest mounts it on the ops listener it starts only for the nats and
    rabbitmq drivers, so with the webhook driver it serves no metrics at all.
    mcp serves none, deliberately (`serve_mcp.go`).
  - **A scrape the chart cannot express.** The single ServiceMonitor targets
    the api Service's `data` port, path `/v1/metrics`: a TLS plane port that
    has no such path. No Service publishes `:9095`, and no ServiceMonitor
    covers the other roles. Every overlay that enables it (dev, staging, prod)
    also keeps `metrics_exporter: otlp`, so there is nothing to scrape in the
    first place.
  - **Switches that must agree and are not made to.** `config.otel.enabled`,
    `config.otel.metrics_exporter`, `config.otel.metrics_addr`,
    `metrics.serviceMonitor.enabled` and the NetworkPolicy scrape rule are
    independent values. The chart renders any combination of them, including
    the ones above that scrape nothing.
  - **Alerts in two homes.** The chart's PrometheusRule carries five api
    rules. The outbox, rate-limit, capability-charge, read-replica, upload and
    worker rules live in `deploy/grafana/*-alerts.yaml`, which no install
    applies: an operator has to copy them in. A default install therefore has
    no alert on a stalled worker or an outbox that does not drain.

  Each of these failed silently. A wrong scrape target is a `down` target
  nobody has a rule for; an alert over a series that does not exist loads
  and never fires (fixed for the RPC alerts in the chart, `fix(chart)` of
  2026-10-06).

- **Decision.**
  - **One endpoint per process.** Every role that builds `SharedDeps` (api,
    admin, worker, dispatcher, ingest) serves `/metrics` on the dedicated
    listener `app.BuildMetricsListener` already provides, at
    `otel.metrics_addr`, and on nothing else. The ops listeners stop mounting
    it. ingest gets it under every driver. mcp stays without one; it holds no
    instruments of its own.
  - **One chart switch.** `metrics.mode: push | scrape | off` replaces
    `metrics.serviceMonitor.*` and the chart's `config.otel.metrics_exporter`
    and `metrics_addr`. The chart renders the exporter (`otlp`, `prometheus`,
    `none`) and the address from it, so the two cannot disagree. `scrape`
    requires `config.otel.enabled: true`, which also gates the MeterProvider;
    the chart refuses the combination rather than render a PodMonitor and
    ports for a listener that never opens. Default `push`, the behaviour
    today; with OTel off it renders nothing, as the traces do.
  - **One scrape object.** In `scrape` mode the chart renders a single
    PodMonitor selecting the release's pods by the container port named
    `metrics`, path `/metrics`, plain HTTP. Every scraped role declares that
    port; no Service changes, and a new role is scraped by declaring it. The
    port name, the port number and the role list each live in one
    `_helpers.tpl` definition that the Deployment, PodMonitor and
    NetworkPolicy templates all read.
  - **Every alert in the chart.** The rule files move under the chart and
    are rendered into the PrometheusRule as templates, so each threshold,
    window and `for` is a value under `metrics.alerts.rules`, and each alert
    links its runbook under `docs/runbooks`. `deploy/grafana` keeps the
    dashboards. The lifecycle rules (crash loop, not ready, OOM) cover every
    role and carry its `component`, instead of matching the api's component
    on one rule and every container named `paladin-core` on the others.
  - **One render test.** A single script renders the chart in each mode and
    asserts the pieces agree: the exporter in the rendered config, the
    container ports, the PodMonitor, the NetworkPolicy scrape rule, the
    refused combinations. It then runs `promtool check rules` and the alert
    unit tests against the rendered PrometheusRule, as
    `chart-alerts.test.sh` does today.

- **Consequences.**
  - The inconsistent configurations become unrepresentable: the class of bug
    behind the ServiceMonitor and the scrape-handler alerts has no switch to
    hide behind.
  - **Breaking values change.** `metrics.serviceMonitor.*`,
    `config.otel.metrics_exporter` and `config.otel.metrics_addr` leave the
    chart's values, and the strict schema refuses them. The dev, staging and
    prod overlays here and the local-iac values for paladin-core change in
    the same release. The overlays that run Prometheus Operator take
    `metrics.mode: scrape`; the PrometheusRule they already deploy assumes
    the series land in that Prometheus.
  - **Ordering.** worker, dispatcher and ingest open the metrics port only
    from the server release that carries the listener change, so the chart
    that scrapes it ships with or after that appVersion. The application
    config schema does not change: `metrics_exporter` and `metrics_addr`
    stay, rendered by the chart instead of written by the operator.
  - Operators without Prometheus Operator lose the copy-in rule files. They
    can render the chart and take `spec.groups` from the PrometheusRule,
    which is the same text the tests exercise.
  - Push mode keeps relying on the collector to deliver metrics to whatever
    evaluates the alerts. That is outside the chart and stays an operator
    concern, as in ADR-0001.

## Plan

Each step is one pull request with its tests, merged in this order.

1. **Server: one metrics listener in every role.** worker, dispatcher and
   ingest append `BuildMetricsListener` to their listeners and drop
   `/metrics` from the ops muxes; ingest serves it under the webhook driver
   too. Unit tests assert, per role, that `/metrics` answers on the metrics
   listener only and that it is absent when the exporter is not
   `prometheus`.
2. **Chart: `metrics.mode` and the PodMonitor.** Helpers for the port name,
   number and scraped roles; the `metrics` container port; the PodMonitor;
   the NetworkPolicy scrape rule on every scraped role; the rendered
   exporter and address; the refused combinations. `servicemonitor.yaml` and
   its values go. Overlays move to `metrics.mode`. Requires the release from
   step 1. Breaking for chart values, so `feat(chart)!`.
3. **Chart: every alert in the PrometheusRule.** Move `paladin-alerts.yaml`
   and `worker-alerts.yaml` under the chart as templated rule files with
   named values and runbook links; make the lifecycle rules per role; delete
   the copies under `deploy/grafana` with their promtool tests, which move
   to the chart's test.
4. **local-iac.** Replace the removed keys in the paladin-core values with
   `metrics.mode`, pinned to the chart release from step 2.
5. **Docs.** `deploy/grafana/README.md`, `backend/docs/observability.md` and
   the runbooks point at the chart's rules and the one endpoint; ADR-0001
   links here.
