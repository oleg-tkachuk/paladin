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

A bare string is accepted as shorthand for `{name: <string>}`.

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
| `logger` | level, encoding, sampling, static fields |
| `otel` | traces and metrics export (ADR-0001); disabled costs nothing |
| `runtime` | process-wide HTTP flags, shutdown timeout, `health_snapshot_token` |
| `api` | the data (`:8080`) and iam (`:8085`) listeners |
| `admin` | the admin listener (`:8090`) |
| `datastores` | Postgres DSN and the separate migrate / reaper credentials |
| `limits` | object and multipart size ceilings, part sizes, content types, presign lifetimes |
| `auth` | JWT signing, token TTLs, login rate limiting |
| `security` | `reject_tenant_mismatch` (`log_sensitive` is retired: accepted, ignored, warned about) |
| `bootstrap` | the platform admin provisioned by `paladin bootstrap` |
| `middleware` | interceptor defaults shared by every plane |
| `worker` | job intervals, leases, reaper batch sizes |
| `dispatcher` | outbox drain loop and sink behaviour |
| `storage` | backends, routing, SSE, per-backend auth mode |
| `ingest` | the storage-notification receiver: driver, webhook, dedup |
| `cedar` | policy engine sources and evaluation |
| `mcp` | the MCP server and its upstreams |
| `capability` | issuer, signing key, verification, budgets |
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

### Storage backend auth modes

`storage.backends.<name>.auth.mode` is mandatory — there is no default,
because an implicit fallback to the AWS credential chain is the kind of
mistake you find in production:

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
sets: `PALADIN_DATA_URL`, `PALADIN_IAM_URL` and `PALADIN_ADMIN_URL`, plus
`PALADIN_HEALTH_SNAPSHOT_TOKEN` (the backend's `runtime.health_snapshot_token`)
and `PALADIN_BFF_MAX_CONNECTIONS`.

`frontend/configs/config.yaml`, read at boot from `/app/configs/config.yaml`
or `configs/config.yaml` and validated with zod (`frontend/src/config.ts`),
carries only build metadata (`runtimeConfig.public.uiMetadata`). It has no
overlay chain and no `PALADIN_` translation.
