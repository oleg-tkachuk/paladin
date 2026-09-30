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
  - [Connect RPC](https://connectrpc.com/): Connect-compatible RPC framework (replaces Gin).
  - [Protobuf + buf/validate](https://buf.build/bufbuild/protovalidate): Contract-first API with declarative validation.
  - [SQLC](https://sqlc.dev/): Type-safe SQL generator.
  - [Google Wire](https://github.com/google/wire): Dependency injection.
  - [CUE](https://cuelang.org/): Configuration schema validation.
  - [Koanf](https://github.com/knadh/koanf): Configuration management.

## Service Topology & Dependencies

### Upstream Dependencies

- **PostgreSQL**: Primary metadata store (objects, tenants, audit logs).
- **S3-compatible Storage (e.g., SeaweedFS)**: Physical object storage.
- **OpenTelemetry Collector**: For distributed tracing and metrics.

### Downstream Consumers

- **Tenant applications**: Consume Paladin for document and asset management.
- **Workflow workers**: Use Paladin for hard deletion and object lifecycle management.
- **Frontend applications**: Directly consume signed URLs for uploads and downloads.

### Data Flow

1. **Metadata Registration**: Metadata for an object is stored in Postgres.
2. **Signed Action**: Paladin provides pre-signed S3 URLs for direct client-to-storage upload/download.
3. **Completion**: Clients notify Paladin when an upload is complete to finalize metadata.
4. **Lifecycle**: Background workers reap expired `PENDING` objects and
   abandoned multipart sessions, drain the purge queue, roll partitions,
   and reconcile quota usage.
5. **Events**: Mutations write to the transactional outbox on the same
   transaction (ADR-0003), and a dispatcher delivers them to subscriptions
   — so a crash can never record the change without its event.

## Transport Layer

RPCs are served over Connect on three planes, each its own service with its
own protobuf package, auth audience and Postgres role:

| Plane | Package | Serves |
|-------|---------|--------|
| **data** | `paladin.data.v1` | Object lifecycle, multipart, presign, batch, tags, operations |
| **admin** | `paladin.admin.v1` | Tenants, buckets, backends, collections, quotas, policy, capabilities, audit, billing |
| **iam** | `paladin.iam.v1` | Auth, users, user settings, health |

Each plane's handlers live under `internal/api/connectshim/<plane>/`, one
file per service — `object_server.go`, `collection_server.go`, and so on.
Those are thin: they translate protobuf to and from the domain types and
delegate to the handlers in `internal/api/v1/<resource>/`, which hold the
logic and know nothing about Connect. That split is what lets the same
handler be driven by the MCP bridge as well as by RPC.

### Shared Components

| Package | Purpose |
|---------|---------|
| `internal/api/v1/apiutil/` | Domain-error → Connect-code mapping (`errmap.go`), the caller/principal context helpers, slug validation, the audit stash |
| `internal/api/connectshim/*/conv.go` | Domain ↔ proto converters, per plane |
| `internal/api/connectshim/resolve/` | Resource-name → binding resolution shared by handlers |

### Interceptor chain

Built per plane in `internal/app/build_listeners_{api,admin}.go` rather than
in one shared chain — the planes accept different credentials and enforce
different audiences. The data plane's order, which carries the most
constraints:

1. **OTel** — trace/span per RPC
2. **Auth (JWT)** — verifies non-PAT bearers; steps aside for a PAT or a
   capability-only request, which carries no `Authorization` header at all
   (ADR-0010: a capability may be the entire credential)
3. **API token** — verifies `paladin_pat_…` and establishes the principal
4. **Capability** — same, for capability credentials
5. **RequireAudience** — must follow 2–4: the audience check reads the
   principal, so anything that *establishes* one has to run first
6. **QuotaSoftCheck** — tenant and bucket scope; the bucket scope resolves
   the upload's Collection to its bucket, without which bucket quota rows
   are maintained and displayed but reject nothing
7. **Validation** — `buf/validate` annotations via `protovalidate`
8. **Idempotency** — reads/writes `idempotency_keys` for replayable writes

Rate limiting (`internal/middleware/ratelimit.go`, a per-tenant token
bucket) and audit (`audit.go`) are wired where they apply rather than into
every chain.

## Runtime Entry Points

One binary, several roles. `cmd/server/` defines a subcommand per role, and
a deployment runs whichever ones it needs — separately in a cluster, or
together on a laptop:

| Command | Role |
|---------|------|
| `serve-api` | Data plane (`paladin.data.v1`) + IAM |
| `serve-admin` | Admin plane (`paladin.admin.v1`) |
| `serve-worker` | Reapers, partition maintainer, purge drainer, quota reconciler |
| `serve-dispatcher` | Outbox drain → sinks |
| `serve-ingest` | Storage-event ingest (SQS / NATS / webhook) |
| `serve-mcp` | MCP bridge |
| `migrate` | Apply the schema baseline |
| `bootstrap` | Create the platform admin |

Composition lives in `internal/app/` — `build_listeners_*.go` assemble the
serving planes, `build_jobs.go` the background workers, `build_deps.go` the
shared dependencies. `internal/wire/wire.go` holds the Wire provider graph.

## Configuration Sources

- **YAML** — `configs/config.yaml` (the annotated reference), with
  `configs/local.yaml` and `configs/compose.yaml` as environment overlays.
- **Environment variables** — prefixed `PALADIN_`. The loader keys on that
  prefix and *ignores* anything else, so a stale prefix goes quiet rather
  than erroring.
- **Kubernetes secrets** — fields with a `_secret` suffix resolve from a
  mounted secret rather than the YAML value.

Every key is validated against `internal/config/schema.cue` at startup. The
loader is strict: an unknown key is a startup failure, not a warning. That
means **a config-schema change must deploy together with the binary that
understands it**, never ahead of it.

See [configuration.md](configuration.md) for the field reference.

## Source Index

- `internal/api/v1/` — Domain handlers per resource (object, tenant,
  collection, multipart, …): the business logic, transport-agnostic.
- `internal/api/connectshim/` — Connect RPC servers that adapt the generated
  protobuf surface onto those handlers, grouped by plane (admin / data / iam).
- `sdk/go/gen/` (repository root) — Generated protobuf and Connect code,
  shared with the Go SDK.
- `internal/store/postgres/` — Repositories. `queries/` holds the sqlc source,
  `sqlc/` the generated code, `adapters/` the domain-facing wrappers.
- `internal/storage/s3adapter/` — S3-compatible backend client.
- `internal/storage/events/` — Inbound storage-event drivers (SQS, NATS, …).
- `internal/eventingest/` — Ingest pipeline: parse, dedup, promote to
  `AVAILABLE`.
- `internal/policy/cedar/` — Cedar engine, policy cache, LISTEN/NOTIFY
  invalidation.
- `internal/capability/` — Capability issuance, verification, usage counters.
- `internal/middleware/` — Interceptors: auth, quota, idempotency, rate limit,
  validation, logging.
- `internal/worker/` — Background jobs: outbox dispatcher, reapers, partition
  maintainer, purge drainer, quota reconciler.
- `internal/statemachine/` — Legal object-state transitions, in one place.
- `internal/app/`, `internal/wire/` — Composition: what each plane builds and
  which dependencies it gets.
- `internal/config/` — Koanf loader + CUE schema.
- `proto/paladin/{admin,data,iam,common}/v1/` — Protobuf service definitions,
  one package per plane.
- `migrations/` — The three-file schema baseline; see
  [database.md](database.md).
- `internal/integration/`, `tests/integration/` — Postgres-backed integration
  suites, both behind the `integration` build tag.

## Related documents

- [database.md](database.md) — schema, RLS, triggers, lifecycle
- [db-roles.md](db-roles.md) — the role split and what each may do
- [API.md](API.md) — RPC surface and HTTP mappings
- [cedar-authoring.md](cedar-authoring.md) — writing policies
- [ops-housekeeping.md](ops-housekeeping.md) — background jobs and their knobs
- [adr/](adr/) — backend architecture decisions
