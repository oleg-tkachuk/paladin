# Runbook: Paladin api error rate high

Covers `PaladinApiHighErrorRate` from the backend chart's PrometheusRule
(`backend/deploy/chart/templates/prometheusrule.yaml`, gated by
`metrics.alerts.enabled`).

| Fires when | Meaning |
|-----------|---------|
| the fraction of Connect RPCs failing with a server-fault code over `window` stays above `threshold` for `for` | Paladin is failing requests it should have served. |

The server-fault codes are `metrics.alerts.rules.highErrorRate.serverErrorCodes`
— by default the ones connect-go answers with an HTTP 5xx: `UNKNOWN`,
`DEADLINE_EXCEEDED`, `UNIMPLEMENTED`, `INTERNAL`, `UNAVAILABLE`, `DATA_LOSS`.
Client faults (`NOT_FOUND`, `PERMISSION_DENIED`, `RESOURCE_EXHAUSTED`, …) do
not count.

## Signal source

otelconnect's `rpc.server.call.duration` histogram, which Prometheus names
`rpc_server_call_duration_seconds_*`. Every call is one observation; a failed
call carries `error_type` set to its upper-case Connect code, and `rpc_method`
names the RPC. The series exist only with `otel.enabled: true`
([observability.md](../../backend/docs/observability.md)).

## Triage

1. **Which RPCs and which codes.** Break the failures down:

   ```
   sum by (rpc_method, error_type) (
     rate(rpc_server_call_duration_seconds_count{error_type=~"UNKNOWN|DEADLINE_EXCEEDED|UNIMPLEMENTED|INTERNAL|UNAVAILABLE|DATA_LOSS"}[5m])
   )
   ```

   The RPC RED dashboard (`deploy/grafana/paladin-rpc-red.json`) shows the same
   per service.

2. **One method or all of them.**
   - Every method, mostly `UNAVAILABLE` / `DEADLINE_EXCEEDED`: a shared
     dependency. Check Postgres (`db_client_operation_errors_total` on the
     Operations dashboard) and the object store.
   - One method, `INTERNAL` / `UNKNOWN`: a handler bug or a bad row. Read the
     api logs for that procedure; every line written while serving an RPC
     carries `trace_id`, which opens the failing span.
   - `UNIMPLEMENTED`: a client calling an RPC this build does not serve —
     usually a client released ahead of the server.

3. **Did it start with a rollout.** Compare the alert's start with the api
   Deployment's last rollout:

   ```
   kubectl --context=<ctx> -n paladin rollout history deploy/paladin-core-api
   ```

   A regression introduced by the new image is rolled back with
   `kubectl rollout undo`, then investigated.

## Escalation / notes

- `warning` by default. Escalate when the failing methods are on the data
  plane's read or write path and the rate keeps climbing.
- The rule covers every Connect listener: the admin plane's failures count
  too. `metrics.alerts.rpcSelector` narrows it where the metrics pipeline adds
  labels that tell the planes apart.
