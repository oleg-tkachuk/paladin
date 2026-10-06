# Runbook: console /stats shows "census unavailable"

The console's **Platform Stats** page (`/stats`) is backed by
`admin/v1.SystemService.GetPlatformStats`, which assembles its answer from
**two** sources:

| Half | Source | Tables |
|------|--------|--------|
| Inventory (tenants, backends, buckets, collections, users) | the **admin** pod's own pool | `tenants`, `storage_backends` and `buckets` are not RLS'd; `collections` and `users` are, but their policy admits the cross-tenant read flag ([migration 002](../../backend/migrations/002_roles_and_rls.sql), [017](../../backend/migrations/017_users_rls.sql)), which the handler sets (`auth.WithCrossTenantRead`) |
| RLS'd census (objects, quotas, capabilities, M2M tokens, event subscriptions) | the **worker** pod's ops listener, proxied | all RLS'd; the worker answers from its BYPASSRLS pool |

The flagged counts on the page (quotas at or near their limit, capabilities
and API tokens expiring) open a drill-down sheet listing the tenants behind
the number. That sheet calls a second RPC,
`SystemService.ListPlatformStatsTenants`, which proxies the worker's
`/system/rls-census/tenants.json?signal=<signal>`.

When the second half can't be reached the RPC still succeeds — it returns
`rls.available=false` and the page renders the inventory cards while every
worker-fed card shows an "unavailable" badge. One flag covers all five
censuses because they share one source and therefore fail together. That is
a degraded view, never an error, so a dead worker can't take the whole page
down.

## Why it degrades

`GetPlatformStats` GETs `<worker.ops_url>/system/rls-census.json`. It reports
unavailable for any of these, without distinguishing them to the caller:

1. **`worker.ops_url` is empty.** The default in
   [`configs/config.yaml`](../../backend/configs/config.yaml) is `""`; the chart
   sets `http://paladin-core-worker:8099`. A deploy running a hand-rolled config
   without the key gets no census.
2. **The worker pod is down or has no endpoints.** Check
   `kubectl get pods -l app.kubernetes.io/component=worker`.
3. **NetworkPolicy blocks admin → worker.** The allow lives in
   [`networkpolicy.yaml`](../../backend/deploy/chart/templates/networkpolicy.yaml)
   and targets **container port 8090**, not the Service port 8099. Only relevant
   when `networkPolicies.enabled=true`.
4. **The worker has no BYPASSRLS pool.** The endpoint answers `503` rather than
   returning zeros, because zeros would read as "the fleet is empty". Set
   `datastores.postgres.migrate_dsn` (or `reaper_dsn`) — see
   [`db-roles.md`](../../backend/docs/db-roles.md).

The drill-down has **no** degraded answer: it is only opened once the census
has shown the count, so an empty list would read as "nobody".
`ListPlatformStatsTenants` returns `UNAVAILABLE` for every case above (an
empty `worker.ops_url`, a dial failure, a non-200 from the worker, a body it
cannot decode), and the sheet shows the error instead of a table
(`PlatformStatsTenants` in
[`systemh/handler.go`](../../backend/internal/api/admin/v1/systemh/handler.go)).
A caller without `platform.admin` gets `PERMISSION_DENIED` before the worker
is asked.

## Triage

The runtime image is distroless — there is no shell or `wget` to `exec`
into. Attach an ephemeral container to an admin pod instead; it shares the
pod's network namespace, so the NetworkPolicy applies to it exactly as it
does to the admin process:

```bash
kubectl -n <ns> debug -i <paladin-core-admin-pod> --image=busybox:1.37 -- \
  wget -qO- http://paladin-core-worker:8099/system/rls-census.json | head -c 400
# the drill-down endpoint
kubectl -n <ns> debug -i <paladin-core-admin-pod> --image=busybox:1.37 -- \
  wget -qO- 'http://paladin-core-worker:8099/system/rls-census/tenants.json?signal=quota_at_limit'
```

`signal` is one of `quota_at_limit`, `quota_near_limit`,
`capabilities_expiring`, `api_tokens_expiring`; anything else answers `400
unknown signal`.

- **JSON body** → the worker leg is healthy; the problem is `worker.ops_url` in
  the admin pod's config (case 1). Compare with
  `kubectl get cm paladin-core-config -o yaml`.
- **`no bypassrls pool`** → case 4. On the census endpoint the worker logs
  `RLS census requested but no BYPASSRLS pool is configured`; on the
  drill-down it logs `RLS census drill-down failed` with
  `signal drill-down requested but no BYPASSRLS pool is configured`.
- **`census unavailable`** (HTTP 500) → the worker reached Postgres but a
  census query failed. The worker logs `RLS census failed` (census) or
  `RLS census drill-down failed` (drill-down) with the error.
- **Connection refused / timeout** → case 2 or 3.

## Cost note

The census is five sequential collectors per poll (`CollectRLS` in
[`platformstats.go`](../../backend/internal/platformstats/platformstats.go);
the page polls every 30s while open, and only while the tab is visible). Four
are cheap aggregates over small operator-managed tables; the objects one is a
`GROUP BY tenant_id, state` over `objects`, which is O(rows). Paging the
per-tenant breakdown does not shrink that scan — the page is cut after the
aggregate. A drill-down is one more `GROUP BY` over the flagged table, run
only when the sheet is opened. There is no cached rollup — see BACKLOG if
that scan starts showing up in `pg_stat_statements`.

## Reading the quota card

`quotas.usage_object_count` / `usage_total_bytes` are a **reconciled
snapshot**, not a live measurement. Two writers maintain them:

- the upload path increments them on promote (`touchQuota`, called from
  `CompleteObject` and `CopyObject` in the object handler), deliberately
  swallowing write errors so a transient DB
  fault cannot undo a committed state transition — which makes the increment
  lossy by design;
- `worker.QuotaReconciler` (`worker.jobs.quota_reconcile.interval`, default
  15m) recomputes them from live `objects` and rolls the per-day counters at
  midnight UTC.

So the counter converges on the object census but can trail it by up to one
reconcile interval. `middleware.QuotaSoftCheck` rejects uploads against these
columns, so they are the right number when debugging an unexpected
`ResourceExhausted`, and the wrong number for "how much are we storing" — the
object census answers that. Quotas are opt-in, so the card also states how
many active tenants the counter covers at all.

If the reconciler is disabled (`interval: 0`) the counters only ever climb
and tenants that delete their objects eventually cannot write. See
[`quota-usage-drift.md`](quota-usage-drift.md).
