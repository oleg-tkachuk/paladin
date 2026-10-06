# Architecture — Paladin backend

## Overview

Paladin is a multi-tenant control plane for object storage. It owns the
metadata — what exists, who may touch it, what happened to it — and hands
out presigned URLs so bytes move directly between the client and the
backend, never through Paladin. Postgres holds every fact; S3 (or any
S3-compatible store) holds only blobs.

Isolation is enforced twice over: Cedar decides *authorisation* at the API
boundary, and Postgres row-level security enforces *tenancy* at the storage
boundary. RLS is a primary control here, not defence in depth — the data
plane runs without `BYPASSRLS`, so a missing policy is a missing wall.

## Inventory Map

- **Language**: Go
- **Frameworks**:
  - [Connect RPC](https://connectrpc.com/): the RPC framework, over HTTP/1.1 and HTTP/2.
  - [Protobuf + buf/validate](https://buf.build/bufbuild/protovalidate): Contract-first API with declarative validation.
  - [SQLC](https://sqlc.dev/): Type-safe SQL generator.
  - [Uber fx](https://github.com/uber-go/fx): dependency injection and lifecycle (`internal/app/appfx.go`).
  - [CUE](https://cuelang.org/): Configuration schema validation.
  - [Koanf](https://github.com/knadh/koanf): Configuration management.

## Service Topology & Dependencies

### Dependencies

- **PostgreSQL** — every fact: tenants, objects, policies, quotas, audit,
  the outbox.
- **S3-compatible storage** — object bytes only (SeaweedFS, MinIO, Garage,
  AWS S3).
- **OpenTelemetry collector** — traces over OTLP; metrics over OTLP or a
  Prometheus scrape (`otel.metrics_exporter`, rendered by the chart from
  `metrics.mode`; ADR-0023).
- **Event sinks** — HTTP, NATS (core or JetStream), Kafka, RabbitMQ, SQS;
  reached by the dispatcher only.

### Consumers

- **Applications and SDKs** — the data plane; bytes go to S3 over presigned
  URLs.
- **The console** — through its BFF, the data, admin and iam planes.
- **AI agents** — through `serve mcp`.

### Data flow

1. **Register.** An upload creates the object row in `PENDING`.
2. **Transfer.** The client moves bytes over a presigned URL.
3. **Complete.** `CompleteObject` (explicit) or a storage notification to
   `serve ingest` (implicit) promotes the object to `AVAILABLE`.
4. **Events.** The state change and its outbox row commit in one
   transaction (ADR-0003); the dispatcher delivers them to subscriptions.
5. **Housekeeping.** Workers reap expired `PENDING` objects and abandoned
   multipart sessions, drain the purge queue, roll partitions and reconcile
   quota usage ([ops-housekeeping.md](ops-housekeeping.md)).

## Transport Layer

RPCs are served over Connect on three planes, each with its own protobuf
package and token audience. All three run as the `paladin_app` database role
([db-roles.md](db-roles.md)):

| Plane | Package | Serves |
|-------|---------|--------|
| **data** | `paladin.data.v1` | Object lifecycle, multipart, presign, batch, tags, operations, storage bootstrap |
| **admin** | `paladin.admin.v1` | Tenants, buckets, backends, collections, quotas, policy, CEL, capabilities, API tokens, tenant budgets, event subscriptions, audit, billing, platform operations, MCP inspection, system |
| **iam** | `paladin.iam.v1` | Auth, users, user settings, health |

Each plane's handlers live under `internal/api/connectshim/<plane>/`, one
file per service — `object_server.go`, `multipart_server.go`, and so on.
Those are thin: they translate protobuf to and from the domain types and
delegate to the handlers in `internal/api/<plane>/v1/<resource>h/`, which
hold the logic and know nothing about Connect. The MCP server does not call
those handlers directly: it goes through Connect clients — over HTTP in bridge
mode, over an in-process transport (`mcp.NewInlineTransport`) with
`--embedded` — so an MCP tool call passes the same interceptor chain as any
other RPC.

### Shared Components

| Package | Purpose |
|---------|---------|
| `internal/api/apiutil/` | Domain-error → Connect-code mapping (`errmap.go`), the caller/principal context helpers, slug validation, the audit stash |
| `internal/api/connectshim/*/conv.go` | Domain ↔ proto converters, per plane |
| `internal/api/connectshim/resolve/` | Resource-name → binding resolution shared by handlers |

### Interceptor chain

Built per plane in `internal/app/build_listeners_{api,admin}.go` rather than
in one shared chain — the planes accept different credentials and enforce
different audiences. The data plane's order, which carries the most
constraints:

1. **OTel** — trace and span per RPC
2. **LogOutcome** — logs failed calls, including auth rejections
3. **Auth (JWT)** — verifies non-PAT bearers; steps aside for a PAT or a
   capability-only request, which carries no `Authorization` header
   (ADR-0010: a capability may be the entire credential)
4. **API token** — verifies `paladin_pat_…` and establishes the principal
5. **Capability** — same, for capability credentials
6. **RequireAudience** — after 3–5, since it reads the principal they set
7. **ActOnNamedTenant** — a platform admin's request acts on the tenant it
   names; everything below keys on that tenant
8. **Tenant rate limit** — per-tenant token bucket, before any database work
9. **Log context** — adds trace, request and tenant ids to handler logs
10. **QuotaSoftCheck** — tenant and bucket scope
11. **Validation** — `buf.validate` rules via `protovalidate`
12. **Idempotency** — `idempotency_keys` for replayable writes
13. **AuditActingElsewhere** — audits a platform admin's calls inside another
    tenant, the ones that tenant's own trail would miss

The admin and iam chains differ in credentials and audience, and each ends in
an audit interceptor (`middleware.AuditWithMirror`) that writes the row for a
mutation before the RPC returns (ADR-0004). The iam chain lets `Login`,
`RefreshToken` and `ExchangeAudience` through anonymously and adds a login
rate limit; the admin chain requires an `Idempotency-Key` on every `Create*` and
`Issue*` RPC.

## Runtime Entry Points

One binary, several roles. `cmd/server/` defines a subcommand per role, and
each role runs as its own process — the chart deploys one Deployment per
role; there is no all-in-one mode:

| Command | Role |
|---------|------|
| `serve api` | Data plane (`paladin.data.v1`) + IAM (`paladin.iam.v1`) |
| `serve admin` | Admin plane (`paladin.admin.v1`) |
| `serve worker` | Background jobs under leases: reapers, purgers, lifecycle, reconcilers, operations; also serves the `/stats` census ([ops-housekeeping.md](ops-housekeeping.md)) |
| `serve dispatcher` | Outbox drain → sinks |
| `serve ingest` | Storage notifications: webhook, NATS, RabbitMQ, SQS |
| `serve mcp` | MCP bridge (`--embedded` hosts the handlers in-process) |
| `migrate` | Apply the migrations |
| `bootstrap` | Create the platform admin and mirror configured storage backends into the database |

Composition lives in `internal/app/` — `build_listeners_*.go` assemble the
serving planes, `build_jobs.go` the background workers, `build_deps.go` the
shared dependencies, and `appfx.go` runs them under Uber fx. `internal/wire/`
holds the plain `Provide*` constructors and the `Repos` / `Storage` seams.

## Configuration Sources

- **YAML** — `configs/config.yaml` (the annotated reference), with
  `configs/local.yaml` and `configs/compose.yaml` as environment overlays.
- **Environment variables** — prefixed `PALADIN_`. The loader keys on that
  prefix and *ignores* anything else, so a stale prefix goes quiet rather
  than erroring.
- **Kubernetes Secrets** — fields with a `_secret` / `_ref` suffix are read
  through the Kubernetes API at boot (`K8sSecretResolver`, using the pod's
  ServiceAccount) and replace the inline value.

Every key is validated against `internal/config/schema.cue` at startup. The
loader is strict: an unknown key is a startup failure, not a warning. That
means **a config-schema change must deploy together with the binary that
understands it**, never ahead of it.

See [configuration.md](configuration.md) for the field reference.

## Source Index

- `internal/api/{admin,data,iam}/v1/<resource>h/` — Domain handlers per
  resource (objecth, tenanth, collectionh, multiparth, …): the business
  logic, transport-agnostic, grouped by the plane that serves them. The `h`
  keeps a handler package apart from the domain package of the same name.
- `internal/api/connectshim/` — Connect RPC servers that adapt the generated
  protobuf surface onto those handlers, grouped by plane (admin / data / iam).
- `sdk/go/gen/` (repository root) — Generated protobuf and Connect code,
  shared with the Go SDK.
- `internal/store/postgres/` — Repositories. `queries/` holds the sqlc source,
  `sqlc/` the generated code, `adapters/` the domain-facing wrappers.
- `internal/storage/s3adapter/` — S3-compatible backend client.
- `internal/eventingest/` — Ingest pipeline: the webhook, NATS, RabbitMQ
  and SQS drivers, parse, dedup, promote to `AVAILABLE`.
- `internal/policy/cedar/` — Cedar engine, policy cache, LISTEN/NOTIFY
  invalidation.
- `internal/capability/postgres/` — The Postgres store behind the standalone
  `capability/` module: records, revocations, usage counters, reservations,
  Biscuit copies.
- `internal/auth/` — The authentication interceptors (JWT, API token,
  capability), audiences, and the principal they establish.
- `internal/middleware/` — The other interceptors: acting tenant, quota,
  idempotency, rate limits, validation, logging, audit, server version.
- `internal/filter/cel/` — CEL list filters, and the subset pushed down into
  the SQL query.
- `internal/platformstats/` — The cross-tenant census for the console's
  `/stats` page and its per-signal tenant drill-down, served from the
  worker's ops listener and proxied by the admin plane.
- `internal/auditstream/` — The live audit stream (`LISTEN paladin_audit`).
- `internal/mcp/` — The MCP server: tools, profiles, bridge and inline
  transports.
- `internal/worker/` — Background jobs and the outbox dispatcher with its
  sinks; `lease/` holds the per-job lease.
- `internal/statemachine/` — Legal object-state transitions, in one place.
- `internal/app/`, `internal/wire/` — Composition: what each plane builds and
  which dependencies it gets.
- `internal/config/` — Koanf loader + CUE schema.
- `proto/paladin/{admin,data,iam,common}/v1/` — Protobuf service definitions,
  one package per plane.
- `migrations/` — goose migrations: the `001`–`003` baseline and
  forward-only changes after it; see [database.md](database.md) and
  [CONVENTIONS.md](../migrations/CONVENTIONS.md).
- `tests/integration/` — Postgres-backed suites behind the `integration`
  build tag: the assembled application at the top level, one component
  against real Postgres or S3 in `components/`. A few packages
  (`internal/worker`, `internal/worker/lease`) carry tagged tests of their
  own; CI runs every package with the tag.

## Related documents

- [database.md](database.md) — schema, RLS, triggers, lifecycle
- [db-roles.md](db-roles.md) — the role split and what each may do
- [API.md](API.md) — RPC surface and HTTP mappings
- [cedar-authoring.md](cedar-authoring.md) — writing policies
- [ops-housekeeping.md](ops-housekeeping.md) — background jobs and their knobs
- [docs/adr/](../../docs/adr/) — architecture decisions
