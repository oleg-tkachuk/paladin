# Runbook: console /stats shows "census unavailable"

The console's **Platform Stats** page (`/stats`) is backed by one RPC,
`admin/v1.SystemService.GetPlatformStats`, which assembles its answer from
**two** sources:

| Half | Source | Tables |
|------|--------|--------|
| Inventory (tenants, backends, buckets, object keys, users) | the **admin** pod's own pool | not RLS'd — [migration 023](../../backend/migrations/023_rls.sql) deliberately leaves these cross-tenant readable |
| RLS'd census (objects, quotas, capabilities, M2M tokens, event subscriptions) | the **worker** pod's ops listener, proxied | all RLS'd, so only a BYPASSRLS pool sees the fleet |

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

## Triage

```bash
kubectl -n <ns> exec deploy/paladin-core-admin -- \
  wget -qO- http://paladin-core-worker:8099/system/rls-census.json | head -c 400
```

- **JSON body** → the worker leg is healthy; the problem is `worker.ops_url` in
  the admin pod's config (case 1). Compare with
  `kubectl get cm paladin-core -o yaml`.
- **`no bypassrls pool`** → case 4. The worker logs
  `RLS census requested but no BYPASSRLS pool is configured` at the same
  moment.
- **Connection refused / timeout** → case 2 or 3.

## Cost note

The census is five sequential aggregates per poll (the page polls every 30s
while open, and only while the tab is visible). Four are cheap counts over
small operator-managed tables; the fifth is a `GROUP BY (tenant_id, state)`
over `objects`, which is O(rows). There is no cached rollup — see BACKLOG if
that scan starts showing up in `pg_stat_statements`.

## Reading the quota card

`quotas.usage_object_count` / `usage_total_bytes` are **not** a measurement.
They are incremented best-effort when an object is promoted (`touchQuota` in
the object and multipart handlers), never decremented on delete, and no job
reconciles them — so they read as "ever admitted", not "currently stored".
Quota *enforcement* compares against these counters, so they are the right
number when debugging a spurious quota rejection; they are the wrong number
for "how much are we storing". The object census answers that, and the card
says so inline. Quotas are also opt-in, so the card states how many of the
active tenants the counter covers at all.
