# Configuration

How the backend loads its configuration (`internal/config`). Every key, with
its default and a comment, is in [`configs/config.yaml`](../configs/config.yaml);
the operator-facing summary is [docs/configuration.md](../../docs/configuration.md).

## Sources, in order

1. **The base file**, `--config` (default `/app/configs/config.yaml`, where
   the chart mounts its ConfigMap).
2. **Overlays**, `PALADIN_CONFIG_OVERLAYS`: a `:`-separated list of files
   merged over the base key by key. `configs/local.yaml` and
   `configs/compose.yaml` are the repository's overlays.
3. **Environment variables** prefixed `PALADIN_`. The name is resolved
   against the schema, so keys containing underscores are reachable:
   `PALADIN_STORAGE_BACKENDS_PRIMARY_PUBLIC_ENDPOINT` sets
   `storage.backends.primary.public_endpoint` (`EnvKeyMapper`).
4. **CUE defaults** from `schema.cue` fill every key left unset.

## Validation

All of it runs at startup; any failure stops the process.

- **Unknown keys** in the merged files are an error, not a warning — a typo
  fails loudly. A config change must therefore deploy with the binary that
  knows the key, not ahead of it.
- **CUE schema** (`schema.cue`): types, enums, ranges.
- **`Config.Validate()`**: cross-field rules.
- **Weak secrets** (`weak_secrets.go`): the development credentials committed
  in this repository are refused unless `app.env` is one of the disposable
  environments.

## Secrets

When `KUBERNETES_SERVICE_HOST` is set, `K8sSecretResolver` reads every
`*_secret` / `*_ref` field from a Kubernetes Secret through the API, using the
pod's ServiceAccount, and replaces the inline value. Each Secret name must be
listed in the chart's `rbac.secretReader.secretNames`. Outside a cluster the
inline values are used. Details: [security.md](security.md#5-secret-management).

## Sections

| Key | Covers |
|---|---|
| `app` | name and environment |
| `logger`, `otel` | logs, traces, metrics ([observability.md](observability.md)) |
| `runtime` | mode, shutdown timeout, probe logging, health snapshot token |
| `api`, `admin` | listeners, timeouts, TLS for each plane |
| `datastores` | PostgreSQL DSNs, pools, the migrate role |
| `limits` | object and multipart sizes, content types, presign lifetimes |
| `auth`, `security` | token signing, lifetimes, login rate limits, tenant checks |
| `bootstrap` | the first platform admin, storage backends mirrored into the database |
| `middleware` | rate limits, idempotency, quota checks |
| `worker`, `dispatcher` | background jobs and outbox delivery ([ops-housekeeping.md](ops-housekeeping.md)) |
| `storage` | storage backends, SSE ([backend-registry.md](backend-registry.md)) |
| `ingest` | storage notifications: driver, webhook, dedup ([storage-ingest.md](../../docs/storage-ingest.md)) |
| `cedar` | policy cache, canonical entity UIDs |
| `mcp` | MCP transports, upstreams, tool profiles |
| `capability`, `api_token` | capability tokens and API tokens |
