# Paladin

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8.svg)](backend/go.mod)

A multi-tenant control plane in front of object storage, built for
workloads where the thing calling you is an agent rather than a person.

Applications do not hold S3 credentials or a bucket name. They hold an
PALADIN credential scoped to a tenant, and PALADIN decides what it may do, routes
the bytes to whichever backend that tenant is on, meters what was spent,
and emits an auditable event trail.

## Why

An orchestrator spawns sub-agents. Each calls tools that cost money.
Every call needs four answers:

- **May it?** Is this operation, on this resource, permitted?
- **Can it afford it?** Has this agent's budget run out?
- **Whose was it?** Which run, spawned by which parent, spent this?
- **Can I stop it now?** An agent is misbehaving — revoke it mid-flight.

A long-lived API key answers none of these. Role-based access answers
only the first, and coarsely.

## The reusable part

If you only want the authorisation primitive, take
[**`capability/`**](capability/) and leave the rest. It is a separate Go
module — budgeted, delegable, individually revocable authority — with no
database driver and no storage SDK anywhere in its dependency graph, a
property CI enforces against the resolved graph rather than against
`go.mod`.

```bash
go get github.com/oleg-tkachuk/paladin/capability
```

You supply storage; a bundled in-memory implementation is enough to get
started. See [`capability/README.md`](capability/README.md).

## What's in the box

- **Multi-tenant object management** — objects, versions, tags, buckets
  and quotas as first-class rows, with Postgres row-level security as a
  primary isolation control rather than a defence-in-depth extra.
- **Layered authorisation** — Cedar policies stored per tenant and
  simulatable before you save them, CEL scope expressions that narrow a
  credential, and capability tokens that can only ever be delegated
  narrower.
- **Pluggable S3 backends** — SeaweedFS, MinIO, Garage, AWS S3. Added,
  disabled and rotated at runtime; objects migrate between them,
  including across backend types.
- **Durable events** — a transactional outbox drained to HTTP, NATS,
  Kafka, RabbitMQ and SQS sinks, plus a crash-durable audit log built the
  same way.
- **An MCP server** — the RPC surface exposed to agents over the Model
  Context Protocol, as an OAuth 2.1 resource server.
- **An admin console** — Next.js, with a BFF so the browser never holds a
  plane URL or an upstream credential.

## Quick start

Needs Docker and [Task](https://taskfile.dev). Nothing else — no cluster,
no credentials to arrange.

```bash
task e2e-up          # every plane + Postgres + storage + the console
```

The console comes up on <http://localhost:3002> (host 3002 → container 3000),
the data plane on `:8080`, admin on `:8090`.

```bash
task e2e-down        # stop, keep volumes
task e2e-clean       # stop and drop volumes
```

Verifying a change:

```bash
task verify-all      # build, test and lint both halves
task --list-all      # every target, across both namespaces
```

Per-half loops:

```bash
task backend:test               task backend:test:integration
task frontend:node:test         task frontend:node:build
```

Building from source additionally needs Go 1.26+, Node 24 and pnpm 11 —
see [CONTRIBUTING.md](CONTRIBUTING.md) for the full list and the
optional hook tooling.

## Where to read next

| | |
| --- | --- |
| [ARCHITECTURE.md](ARCHITECTURE.md) | what the pieces are and why the boundaries fall where they do |
| [backend/README.md](backend/README.md) | roles, ports, packages, wire contracts, database |
| [frontend/README.md](frontend/README.md) | the console and its BFF |
| [capability/README.md](capability/README.md) | the standalone authorisation primitive |
| [docs/](docs/) | configuration reference, ADRs, runbooks |
| [CONTRIBUTING.md](CONTRIBUTING.md) | conventions, test tiers, how a PR is expected to look |
| [SECURITY.md](SECURITY.md) | the security model, and how to report a vulnerability |

## Layout

```
backend/        Go control plane — one binary, one Deployment per role
frontend/       Next.js BFF + admin console
capability/     standalone Go module: budgeted, delegable authority
docs/           configuration reference, ADRs, runbooks
specs/          spec-driven-development artifacts, per feature
tasks/          cross-project Taskfiles
BACKLOG.md      deferred work, with reasons
```

Both halves live in one repository because a change to the wire contract
is a change to both: the proto in `backend/proto/` is the source of
truth, and the console regenerates its Connect-ES stubs from it.

## Project status

Pre-1.0 and moving. Backward compatibility is not maintained across
releases yet, and only `main` is supported.

[BACKLOG.md](BACKLOG.md) is the register of everything deliberately
deferred — each entry carries its reason, its definition of done, and
what is blocking it. It is long on purpose. Read it before concluding
that a gap is an oversight; quite often it is a decision.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
