# Configuration

Every Paladin process reads one YAML file, optionally layered with overlays,
optionally overridden by environment variables, and then validated three
separate ways before the process starts. This page is the map.

## Where configuration comes from

In order, later wins:

1. **Base file** — `--config <path>`, default `/app/configs/config.yaml`.
   Must contain every required field.
2. **Overlays** — `PALADIN_CONFIG_OVERLAYS`, a colon-separated list of paths.
   Each may be a partial tree carrying only its deltas. The common
   Kubernetes shape is a shared base plus an environment overlay plus a
   secrets overlay:

   ```
   paladin serve api --config /etc/paladin/base.yaml
   PALADIN_CONFIG_OVERLAYS=/etc/paladin/staging.yaml:/etc/paladin/secrets.yaml
   ```

   Note the asymmetry: the base path is a flag, only the overlay chain is
   an environment variable. There is no `PALADIN_CONFIG_PATH`.
3. **Environment variables** — `PALADIN_`-prefixed, resolved against the schema.
4. **CUE defaults** — the schema fills anything still unset.

The files that ship in the repository:

| File | Role |
| --- | --- |
| `backend/configs/config.yaml` | the base; every key, documented inline |
| `backend/configs/local.yaml` | overlay for `go run` on a laptop |
| `backend/configs/compose.yaml` | overlay for the compose stacks, dev-loop and e2e |
| `backend/deploy/chart/values-*.yaml` | Helm renders these into a ConfigMap |

## Environment overrides

The variable name is matched against the schema rather than transliterated
(`EnvKeyMapper` in `backend/internal/config/strict.go`): split on `_`, then
take the longest known field name at each step. Keys with underscores of their
own are therefore reachable:

```bash
PALADIN_STORAGE_BACKENDS_PRIMARY_PUBLIC_ENDPOINT=https://s3.example.com   # → storage.backends.primary.public_endpoint
PALADIN_AUTH_SIGNING_KEY=…                                                # → auth.signing_key
```

## Secrets

Any credential field has a `_secret` sibling that resolves from a
Kubernetes Secret at boot:

```yaml
auth:
  signing_key_secret:
    name: paladin-auth
    key: signing-key        # defaults to "password" when omitted
    namespace: paladin          # defaults to the pod's namespace
```

A bare string is accepted as shorthand for `{name: <string>}`. A few
fields take the same reference under a `_ref` name instead
(`ingest.webhook.shared_secret_ref`, `ingest.nats.token_ref`,
`ingest.rabbitmq.url_ref`, the OAuth `client_secret_ref`s). Each takes a value
or a reference, never both.

Resolution happens only when `KUBERNETES_SERVICE_HOST` is set:
`K8sSecretResolver` (`backend/internal/config/resolver.go`) reads each
referenced Secret through the API with the pod's ServiceAccount and replaces
the inline value. Every Secret name must be listed in the chart's
`rbac.secretReader.secretNames`. Outside a cluster the references are not
resolved and the inline values are used. See also
[security.md](../backend/docs/security.md#5-secret-management).

**Setting both the inline field and its `_secret` sibling is a load
error.** Which one wins is not a question the runtime should have to
answer, so it refuses instead of picking.

### Committed credentials will not boot a real environment

The defaults shipped in this repository — `dev-secret-change-me-32-bytes-min`,
`admin-dev-password-change-me`, the e2e fixture keys — are public. The
loader rejects them, and any value carrying a placeholder marker
(`change-me`, `<prod-…>`, `not-a-secret`, `dummy`), unless `app.env` is
one of the allow-listed disposable environments: `local`, `dev`,
`development`, `test`, `ci`, `e2e`, or unset.

An unrecognised `app.env` is treated as real and refuses. That is
deliberate — a new environment name defaults to safe, not to convenient.
The list and the reasoning live in
[`backend/internal/config/weak_secrets.go`](../backend/internal/config/weak_secrets.go).

## Validation

Three independent gates, each catching what the others cannot:

1. **CUE schema** (`backend/internal/config/schema.cue`) — types, ranges,
   enums, and default injection.
2. **Strict unknown-key check** — a key with no matching struct field
   fails the load, listing every offender. A typo'd option that silently
   has no effect is a worse outcome than a refused start. Runs against
   the *merged* tree, so an overlay that omits most of the schema is not
   penalised for keys it never set.
3. **`Config.Validate()`** — cross-field invariants that a schema cannot
   express: required fields, inline-vs-secret conflicts, per-mode storage
   backend rules, the ingest webhook's authentication requirement outside
   dev, and the weak-secret gate.

Note that the strict-key check does **not** cover environment overrides.
An `PALADIN_` variable that maps to no key is ignored rather than rejected.

## The blocks

`backend/configs/config.yaml` is the reference: every key is there with a
comment. What each top-level block owns:

| Block | Owns |
| --- | --- |
| `app` | name and `env` — `env` drives several safety gates, so it is not cosmetic |
| `logger` | level, encoding, sampling, static fields ([observability.md](../backend/docs/observability.md)) |
| `otel` | traces and metrics export (ADR-0001); disabled costs nothing |
| `runtime` | process-wide HTTP flags, shutdown timeout, `health_snapshot_token` |
| `api` | the data and iam (`:8085`) listeners; data is `:8080` in the chart and compose, `:8083` by default on the host (`#LocalDataPort` in `schema.cue`) |
| `admin` | the admin listener (`:8090`), and `tenants.trash_retention` — how long a trashed tenant waits before it is surfaced for purge ([deletion-semantics.md](deletion-semantics.md#tenant)) |
| `datastores` | Postgres DSN, the separate migrate / reaper credentials, the opt-in read replica |
| `limits` | object and multipart size ceilings, part sizes, content types, presign lifetimes |
| `auth` | JWT signing, token TTLs, login rate limiting, the OAuth 2.1 authorization server (`auth.oauth`, ADR-0009) |
| `security` | nothing to set: `log_sensitive` is retired (accepted, ignored, warned about) |
| `bootstrap` | the platform admin provisioned by `paladin bootstrap` |
| `middleware` | interceptor defaults shared by every plane; the per-tenant rate limit is counted in Postgres, and its `burst`, `max_tenants` and `cleanup_*` keys are accepted but no longer read |
| `worker` | job intervals, leases, reaper batch sizes ([ops-housekeeping.md](../backend/docs/ops-housekeeping.md)) |
| `dispatcher` | outbox drain loop and sink behaviour; every delivery is a CloudEvents 1.0 envelope ([event-delivery-dedup.md](event-delivery-dedup.md)) |
| `storage` | backends, routing, SSE, per-backend auth mode ([backend-registry.md](../backend/docs/backend-registry.md)) |
| `ingest` | the storage-notification receiver: driver, webhook, dedup ([storage-ingest.md](storage-ingest.md)) |
| `cedar` | policy cache TTL, canonical collection entity UIDs ([cedar-authoring.md](../backend/docs/cedar-authoring.md)) |
| `mcp` | the MCP server and its upstreams, its OAuth resource-server posture and token exchange for a delegated capability (ADR-0041), tool profiles and `always_deny`, the bridge's DPoP key |
| `capability` | issuer, signing key, verification, budgets, DPoP server nonces |
| `api_token` | the HMAC key for token lookup digests |

### Client address behind proxies

Each listener (`api.server.data`, `api.server.iam`, `admin.server`) resolves
the client address from `real_ip_header` (default `X-Forwarded-For`). The
same address is Cedar's `context.ip`, the address a capability's
`SourceIPCIDR` caveat is checked against, the per-IP key of the login rate
limiter, and the audit log's source. The header is read only when the TCP
peer is in `trusted_proxies` (CIDRs or addresses), and the chain is walked from
the right past every trusted hop: the first address that is not a trusted
proxy is the client. A leftmost entry the client wrote itself is never taken.
Nothing is specific to one ingress; any proxy that appends to the header works.

`trusted_proxies: []` ignores the header, and the peer is the client. The chart
defaults to the private ranges, which trusts every in-cluster hop — the
ingress controller and the console's BFF, which forwards the header unchanged
on every call it makes to a plane.
Narrow it to the ingress and pod networks where clients can sit inside the
private ranges, or such a client can name its own address.

### Read replica

`datastores.postgres.replica` is off by default, and off means no replica pool
is opened at all. Turned on, listing, counting and searching objects
(`ListObjects`, `CountObjects`, `ListDistinctTags`) read from it; everything
else — authorization, capability and token checks, every write and every
read that follows one — stays on the primary, because a revoked credential
has to be refused on the very next request.

```yaml
datastores:
  postgres:
    replica:
      enabled: true      # the only key CloudNativePG needs
      dsn: ""            # "" → `<cluster>-rw` in `dsn` becomes `<cluster>-ro`
      max_lag: 2s        # further behind than this → reads go to the primary
      lag_check_period: 5s
```

On CloudNativePG nothing else is required. CNPG creates its standbys,
seeds them from the primary and keeps them streaming; the schema, the RLS
policies and the roles arrive with the WAL, so `paladin migrate` never runs
against a replica. The `-ro` service spreads reads over the standbys and is
in the server certificate, so `sslmode=verify-full` keeps working. The
password defaults to the primary's — a physical standby has the same roles.
With a single-instance cluster the `-ro` service has no endpoints, and reads
simply stay on the primary.

Every long-lived role measures the replica's lag each `lag_check_period`.
Reads move to it only once it is within `max_lag`, and back to the primary
whenever it is unreachable, behind, or its lag cannot be told. A replica
added to a running environment therefore catches up on its own and starts
taking reads by itself; a query the replica fails (a conflict with WAL
replay, a restart) is retried on the primary. Lists may trail writes by up
to `max_lag`; set `0s` to drop the bound and keep only the reachability
check.

What it is doing is visible three ways: the health page's `postgres-replica`
row (never critical — a replica that is down fails nothing, so it never fails
readiness) says why reads are on the primary; the metrics
`paladin_db_replica_in_sync`, `paladin_db_replica_lag_seconds` and
`paladin_db_replica_reads_total{served_by,reason}` show it over time; and
`PaladinReadReplicaOutOfSync` (the backend chart's PrometheusRule) fires when
no pod has used the replica for 15 minutes.

Outside CNPG set `dsn` explicitly. The replica must be a physical standby,
or a managed reader endpoint over one; a logical replica does not carry the
roles and policies and is not supported.

### Storage backend auth modes

`storage.backends.<name>.auth.mode` selects how a backend authenticates. It
has no default: a backend that names none fails to load, rather than taking
whatever the AWS SDK's default credential chain finds.

| Mode | Requires | Rejects |
| --- | --- | --- |
| `static_keys` | `access_key` + `secret_key` (or their `_secret` siblings) | `role_arn` |
| `default_chain` | nothing — env vars or instance role | any static key, `role_arn` |
| `assume_role` | `role_arn` | — |
| `web_identity` | `role_arn` | — |

`sse.type: aws:kms` additionally requires `sse.key_id`.

Backends also carry two endpoints. `endpoint` is what the control plane
connects to; `public_endpoint` is what presigned URLs are signed for, and
it matters whenever the browser reaches storage by a different name than
the plane does. Leaving `public_endpoint` empty reuses `endpoint`.

## Frontend configuration

The console's BFF takes its upstreams from environment variables the chart
sets: `PALADIN_DATA_URL`, `PALADIN_IAM_URL` and `PALADIN_ADMIN_URL` for the
three planes; `PALADIN_WORKER_URL`, `PALADIN_DISPATCHER_URL`,
`PALADIN_INGEST_URL`, `PALADIN_MCP_URL` and `PALADIN_HEALTH_ROLES` for the
health aggregator; and the health snapshot token (the backend's
`runtime.health_snapshot_token`). `PALADIN_BFF_MAX_CONNECTIONS` caps the
BFF's upstream connections (default 16, `frontend/src/lib/server/upstream.ts`);
the chart does not set it, so it comes through `extraEnv`.

The chart mounts the health snapshot token from
`healthSnapshotTokenSecret` as a file and names it in
`PALADIN_HEALTH_SNAPSHOT_TOKEN_FILE`, so it is not in the pod's environment;
`PALADIN_HEALTH_SNAPSHOT_TOKEN` carries it inline for local development.

In production both charts read the token from one Secret: the backend through
`runtime.health_snapshot_token_secret`, resolved at boot like every other
`*_secret` reference, and the console through `healthSnapshotTokenSecret`.
`scripts/health-token-secret.test.sh` checks the rendered production manifests
agree.

### Platform banner

A message across every page of the console — a maintenance window, a
migration, a policy change — after Argo CD's `ui.bannercontent`. The chart's
`banner` value sets it, through these variables:

| Chart value | Variable | Default | Meaning |
| --- | --- | --- | --- |
| `banner.content` | `PALADIN_BANNER_CONTENT` | `""` | the message; empty shows no banner |
| `banner.severity` | `PALADIN_BANNER_SEVERITY` | `info` | `info`, `warning` or `critical`: the banner's colour and glyph |
| `banner.url` | `PALADIN_BANNER_URL` | `""` | where "Read more" goes: an `http(s)` address or a path in the console |
| `banner.permanent` | `PALADIN_BANNER_PERMANENT` | `false` | `true`: nobody can dismiss it |
| `banner.position` | `PALADIN_BANNER_POSITION` | `top` | `top`, `bottom` or `both` |

```yaml
banner:
  content: "Storage is read-only on Sunday from 02:00 to 04:00 UTC."
  severity: warning
  url: https://status.example.com
```

The BFF reads the variables on every `/api/shell` poll
(`frontend/src/lib/banner.ts`), so a new banner is a rollout, not a rebuild,
and it stays up while the planes it announces are down. It shows once the
reader is signed in. A banner that is not permanent is dismissed per user:
the dismissal is kept in their settings (`console.dismissedNotices`) under a
hash of the banner's text, link and severity, so it follows them to another
browser and shows again when any of those changes. An unknown severity reads
as `info`, an unknown position as `top`, and a link that is neither `http(s)`
nor a console path is dropped.

### Resource notices

A tenant or a bucket can carry a notice of its own, after Argo CD's
`notice.argocd.argoproj.io/*` annotations: a banner on its pages and a glyph
with a tooltip in its list row. It is kept in the resource's labels:

| Label | Meaning |
| --- | --- |
| `notice.paladin.io/content` | the message; without it there is no notice |
| `notice.paladin.io/severity` | `info` (default), `warning` or `critical` |
| `notice.paladin.io/url` | where "Read more" goes, as for the banner |
| `notice.paladin.io/permanent` | `true`: nobody can dismiss it |

A tenant's notice is set in the console's Edit tenant dialog. A bucket's is
set through `UpdateBucket` with `labels` in the update mask, which replaces
every label, so send the ones it has as well. Content past 500 characters is
cut. Dismissals are remembered like the banner's. Storage backends carry no
labels, so they have no notice.

### Build metadata

`frontend/configs/config.yaml`, read at boot from `/app/configs/config.yaml`
or `configs/config.yaml` and validated with zod (`frontend/src/config.ts`),
carries only build metadata (`runtimeConfig.public.uiMetadata`). It has no
overlay chain and no `PALADIN_` translation.
