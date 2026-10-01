# Paladin backend

Go control plane: one binary, several modes. `paladin serve <role>` runs a
plane or a worker; `paladin migrate` and `paladin bootstrap` are one-shot
lifecycle commands. The Helm chart deploys one Deployment per role, so
the process boundaries below are also the pod boundaries.

## Roles

| Command | Listens on | What it does |
| --- | --- | --- |
| `paladin serve api` | `:8080` data (`:8083` on the host), `:8085` iam | Tenant-facing Connect-RPC. Objects, buckets, tags, presign, multipart, batch, auth. |
| `paladin serve admin` | `:8090` | Platform-facing Connect-RPC. Tenants, quotas, policies, capabilities, API tokens, audit, billing, backends. |
| `paladin serve worker` | `:8099` ops | Background jobs, lease-coordinated: object lifecycle transitions, reapers, quota reconciliation, storage migration. |
| `paladin serve dispatcher` | `:8099` ops | Durable event fan-out. Drains the transactional outbox to webhook and broker sinks. |
| `paladin serve ingest` | `:8099` ops | Receives storage-side events (webhook or JetStream) and promotes objects. Refuses to start when `ingest.enabled` is false. |
| `paladin serve mcp` | `:8095` | Model Context Protocol server — stdio or streamable HTTP — exposing the RPC surface to agents. |
| `paladin migrate` | — | Applies the embedded SQL migrations. Exits 0. |
| `paladin bootstrap` | — | Provisions the platform admin and mirrors configured storage backends into the database. Idempotent. |

The collapsed "everything in one process" mode is gone. `serve mcp` is
the one role that spans planes: it is a superset of `api` + `admin`
without their TCP listeners.

Every role serves `/livez`, `/readyz` and `/startupz`, plus a
`/system/health.json` snapshot that `runtime.health_snapshot_token`
gates.

## Layout

```
backend/
├── cmd/server/       one file per subcommand; fx wiring lives in fxboot.go
├── internal/
│   ├── api/          Connect handlers, one package per service
│   ├── app/          fx modules — the composition root for each role
│   ├── auth/         JWT, API tokens, capability principals, context plumbing
│   ├── policy/       Cedar engine + CEL scope evaluation
│   ├── capability/   the Paladin-side adapter over the standalone capability module
│   ├── store/        sqlc-generated queries + hand-written SQL mapping
│   ├── storage/      S3 backend routing, presign, multipart, migration
│   ├── worker/       background jobs and their leases
│   ├── eventingest/  storage-event intake
│   ├── mcp/          MCP server and tool definitions
│   └── config/       koanf loading, CUE schema, validation
├── migrations/       goose-style numbered SQL, embedded into the binary
├── proto/            source of truth for every wire contract
├── policies/         Cedar policy sources, incl. examples/
└── deploy/           Dockerfile, docker-compose.yaml, Helm chart
```

## Wire contracts

`proto/` is the source of truth. Three API surfaces:

- `paladin/data/v1` — object, tag, presign, multipart, batch, operation,
  storage bootstrap.
- `paladin/admin/v1` — tenant, bucket, collection, quota, capability, API
  token, policy, audit, billing, tenant budget, event subscription,
  backend, MCP inspection, CEL, operation, system.
- `paladin/iam/v1` — auth, system.

Regenerate Go stubs with `task backend:generate`. The frontend regenerates its
Connect-ES stubs from the same directory via `cd frontend && pnpm run
generate` — proto changes are cross-cutting, which is why both halves
live in one repository.

## Database

Postgres, with row-level security as a primary isolation control rather
than a defence-in-depth extra. Connection pools are split by trust level:
the RLS pool carries the tenant context, and the reaper/BYPASSRLS pool is
deliberately separate so a background job cannot inherit a request's
tenant scope.

Three migrations — schema, roles + RLS, triggers — embedded in the binary
and applied by `paladin migrate`. They are a baseline, not a history: the
down migration on `001` drops the schema, so the supported path from an
older database is to reprovision, not to migrate. `migrations/CONVENTIONS.md`
documents the rules; [`docs/database.md`](docs/database.md) walks the schema
itself.

Queries are sqlc-generated where they can be; the store layer's
hand-written SQL mapping is the largest single source of subtle bugs in
the project, which is why the constitution requires integration coverage
for it.

## Local loop

```bash
go build ./...                     # from backend/
task backend:test                  # unit
task backend:test:integration      # testcontainers Postgres, needs Docker
task backend:lint                  # golangci-lint, curated set in .golangci.yaml
task backend:generate              # sqlc + mocks; proto stubs live in sdk/go
```

The full compose stack — every plane, Postgres, SeaweedFS, and the
console — comes up from the repository root:

```bash
task stack:up
```

### Not `go install`-able

The module is `github.com/oleg-tkachuk/paladin/backend`, matching its
directory, but it is not tagged as a Go module and it resolves its sibling
modules through `replace` directives, which `go install …@version` refuses.

**Container images are the distribution path** — build from
`deploy/Dockerfile`, or clone and `go build ./...` from here.

## Configuration

`configs/config.yaml` is the base; `configs/local.yaml` and
`configs/compose.yaml` are overlays. Keys are validated three ways: a CUE
schema, a strict unknown-key check that fails on typos rather than
ignoring them, and `Config.Validate()` for cross-field invariants
([docs/configuration.md](docs/configuration.md)).

Environment overrides use the `PALADIN_` prefix. The name is resolved against
the schema, so keys with underscores of their own work too:
`PALADIN_STORAGE_BACKENDS_PRIMARY_PUBLIC_ENDPOINT` sets
`storage.backends.primary.public_endpoint`. Extra files can be layered over
`--config` with `PALADIN_CONFIG_OVERLAYS`.

Committed credentials are development defaults and are rejected outside
an allow-listed disposable `app.env`; see `internal/config/weak_secrets.go`.

Full reference: [`docs/configuration.md`](../docs/configuration.md).

## The capability module

[`capability/`](../capability/) is a separate Go module, consumed here
through a `replace` directive. It has no database driver and no storage
SDK in its dependency graph — the resolved graph, not `go.mod`: a
transitive pull disqualifies it just as much as a direct one. The
module's `isolation_test.go` asserts this, and runs in
`task -t Taskfile.dev.yaml verify-capability`, part of `verify-all`.

If you are adding code that needs Postgres or S3, it belongs in
`internal/`, not in the module.
