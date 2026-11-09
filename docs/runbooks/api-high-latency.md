# Runbook: Paladin api latency high

Covers `PaladinApiHighLatency` from the backend chart's PrometheusRule
(`backend/deploy/chart/templates/prometheusrule.yaml`, gated by
`metrics.alerts.enabled`).

| Fires when | Meaning |
|-----------|---------|
| the `quantile` of Connect RPC duration over `window` stays above `thresholdSeconds` for `for` | The slowest calls are slow enough for clients to notice or time out. |

## Signal source

otelconnect's `rpc.server.call.duration` histogram, which Prometheus names
`rpc_server_call_duration_seconds_bucket`, labelled by `rpc_method`. The
buckets are the semantic-convention defaults, closing at 0.75, 1, 2.5 and 5s
in the range the thresholds use; a quantile between two bounds is
interpolated, so read the alert's value as "within that bucket", not to the
millisecond. The series exist only with `otel.enabled: true`
([observability.md](../../backend/docs/observability.md)).

## Triage

1. **Which RPCs are slow.** The alert aggregates every method; split it:

   ```
   histogram_quantile(0.99,
     sum by (le, rpc_method) (rate(rpc_server_call_duration_seconds_bucket[5m])))
   ```

   The RPC RED dashboard (`deploy/grafana/paladin-rpc-red.json`) has the same
   per-method p99 panel.

2. **Is it a dependency.** Paladin's handlers wait mostly on Postgres and the
   object store:
   - Postgres: `db_client_operation_duration_seconds` on the Operations
     dashboard, and long-running statements in `pg_stat_activity`.
   - Object store: `paladin_storage_call_duration_seconds` and
     `paladin_presign_duration_seconds`.

3. **Is it one tenant's charges queueing.** Every charge of a tenant locks
   its budget row, so parallel charges on one tenant wait for each other:

   ```
   histogram_quantile(0.99,
     sum by (le, limes_operation) (rate(limes_tenant_wait_seconds_bucket[5m])))
   ```

   The metric names no tenant; limes logs each wait over 100ms at WARN
   (`a call waited long for its tenant's aggregate`) with the tenant's id.
   - A tenant with no ceiling: limes counts it in stripes, several rows,
     so its charges do not queue on one; a wait for such a tenant points
     elsewhere.
   - A tenant with a ceiling: stripes do not apply. The client that charges
     in parallel takes a budget lease per capability
     (`CapabilityService.Lease`, needs `capability.budget_leases`); a charge
     under a lease locks none of the tenant's rows. On the dev cluster, 32
     parallel charges on a capped tenant ran at about 300/s through the
     tenant's row and about 2 800/s under leases.

4. **Streaming RPCs.** A server-streaming call's duration is the life of the
   stream, not the time to first message. If only a streaming method is
   "slow", check whether its clients simply hold streams open longer.

5. **Is it load.** Compare request rate with the api pods' CPU and their HPA:

   ```
   kubectl --context=<ctx> -n paladin get hpa,pods -l app.kubernetes.io/component=api
   ```

   Pods pinned at their CPU limit with the HPA at `maxReplicas` mean the
   ceiling, not the code, is the problem.

## Escalation / notes

- `warning` by default. Escalate when latency is rising with
  `PaladinApiHighErrorRate` firing beside it — `DEADLINE_EXCEEDED` is the same
  slowness seen from the client's side.
- The rule covers every Connect listener, the admin plane included;
  `metrics.alerts.rpcSelector` narrows it.
