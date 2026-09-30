# paladin

[![ci](https://github.com/oleg-tkachuk/paladin/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/oleg-tkachuk/paladin/actions/workflows/ci.yaml)

[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Connect RPC](https://img.shields.io/badge/Connect%20RPC-1D4ED8)](https://connectrpc.com)
[![Cedar](https://img.shields.io/badge/Cedar-FF9900)](https://www.cedarpolicy.com)
[![MCP](https://img.shields.io/badge/MCP-000000?logo=modelcontextprotocol&logoColor=white)](https://modelcontextprotocol.io)
[![Next.js](https://img.shields.io/badge/Next.js-000000?logo=nextdotjs&logoColor=white)](https://nextjs.org)

A multi-tenant control plane in front of object storage, built for workloads
where the caller is an agent rather than a person. Applications hold no S3
credentials and no bucket name: they hold a Paladin credential scoped to a
tenant, and Paladin decides what it may do, routes the bytes to whichever
backend that tenant is on, meters what was spent, and emits an auditable
event trail.

An orchestrator spawns sub-agents, and every call they make needs four
answers: may it, can it afford it, whose spend was it, and can it be stopped
now. A long-lived API key answers none of them. The primitive that does —
budgeted, delegable, individually revocable authority — is
[`capability/`](capability/), a separate Go module with no database driver
and no storage SDK among its dependencies, usable without the rest of Paladin.

```
backend/      one Go binary (paladin serve <role>), one Deployment per role
  ├─ api          data plane + IAM
  ├─ admin        tenants, quotas, policies, capabilities, backends
  ├─ mcp          api + admin, exposed to agents over the Model Context Protocol
  ├─ worker       lifecycle, reapers, quota reconciliation, storage migration
  ├─ dispatcher   transactional outbox → webhook and broker sinks
  ├─ ingest       storage-side events → objects
  ├─ Postgres     state, with row-level security per tenant
  └─ S3           SeaweedFS, MinIO, Garage or AWS S3, switchable at runtime
capability/   standalone module: capability tokens ◄── imported by backend/
frontend/     Next.js console + BFF ──Connect RPC──► api, admin
```

## Contents

- [Quick start](#quick-start) — the whole stack, locally
- [Prerequisites](#prerequisites) — what has to be installed
- [Commands](#commands) — the handful worth knowing
- [How changes land](#how-changes-land) — trunk, CI and releases
- [Documentation](#documentation) — the rest, by document
- [Layout](#layout) — where things live in the tree
- [License](#license)

## Quick start

Running the stack needs Docker and [Task](https://taskfile.dev) — no cluster
and no credentials to arrange.

```bash
git clone https://github.com/oleg-tkachuk/paladin.git && cd paladin
task stack:up        # every plane + Postgres + SeaweedFS + the console, in compose
```

The console is on <http://localhost:3002>, the data plane on `:8083` (not
`:8080`, which another local service uses), admin on `:8090`. The compose file,
[`backend/deploy/docker-compose.yaml`](backend/deploy/docker-compose.yaml),
lists the rest.

```bash
task stack:down      # stop, keep volumes
task stack:reset     # stop and delete volumes
```

Working on the code rather than running it is the other entry point:

```bash
task -t Taskfile.dev.yaml             # the gates, codegen and dependency bumps
task -t Taskfile.dev.yaml verify-all  # the commit gate: tests, lint and build
```

## Prerequisites

`task stack:up` needs only the first two rows. Working on the code needs the
rest.

| Tool | Why |
|------|-----|
| [Docker](https://docs.docker.com/get-started/get-docker/) | the compose stack, testcontainers, `verify-deep` and `verify-e2e` |
| [Task](https://taskfile.dev/installation/) 3.53+ | every entry point; CI pins 3.53.1, and the shared [task library](https://github.com/oleg-tkachuk/taskfiles) is a remote include |
| [Go](https://go.dev/dl/) 1.27+ | `backend/` and `capability/`, per their `go.mod` |
| [Node](https://nodejs.org) 26 | the console; the version its image ships and CI verifies with |
| [pnpm](https://pnpm.io) 12.7 | pinned by `packageManager` in `frontend/package.json`; Node 26 has no corepack, so `npm install -g pnpm@12.7.0` |

`verify-all` also shells out to golangci-lint, buf, helm, yq and
python3. On macOS `brew bundle` installs that set; the [Brewfile](Brewfile)
says which tools are pinned elsewhere instead, and why.
[docs/development.md](docs/development.md) covers the optional git hooks.

## Commands

Three entry points, split by who runs the command. `task` operates the stack;
`task -t Taskfile.local.yaml` publishes to the host-only `registry.local` and
syncs the local ArgoCD applications; `task -t Taskfile.dev.yaml` works on the
repository. Each on its own prints its handful of commands, and `--list` after
any of them prints everything it reaches.

Releases are published by CI: for every release tag,
[release.yaml](.github/workflows/release.yaml) pushes both images
(linux/amd64 and linux/arm64) and both Helm charts to GHCR, under the
repository owner.

| Task | Does |
|------|------|
| `task stack:up` | the whole stack in compose, waiting until every service is healthy |
| `task -t Taskfile.local.yaml deploy` | build and publish both images and charts to `registry.local` |
| `task -t Taskfile.local.yaml deploy:sync` | hard-refresh the local ArgoCD applications after a deploy |
| `task -t Taskfile.dev.yaml verify-all` | the commit gate: every tree's tests and lint, the proto compatibility check, the chart and Taskfile contract checks, the console build; no Docker |
| `task -t Taskfile.dev.yaml verify-deep` | the Postgres-backed integration suites and the gates needing a live stack; Docker, ~15 min |
| `task -t Taskfile.dev.yaml verify-e2e` | Playwright against images built from the current branch; Docker, ~10 min |

Per half, from any of them: `task backend:test`, `task backend:test:integration`,
`task frontend:test`, `task frontend:build`.

## How changes land

`main` is the only long-lived branch. Changes reach it through pull requests,
with [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

[`ci.yaml`](.github/workflows/ci.yaml) runs `verify-all` and audits the
workflows with actionlint and zizmor. A green push to `main` dispatches
[`release.yaml`](.github/workflows/release.yaml): semantic-release computes
the next tag from the commits since the last one, and a GitHub release with
generated notes is created for it.

CI builds no image, publishes no chart and deploys nothing. `verify-deep` and
`verify-e2e` need Docker and run locally, before a merge.

## Documentation

| Document | Covers |
|----------|--------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | what the pieces are and why the boundaries fall where they do |
| [backend/README.md](backend/README.md) | roles, ports, packages, wire contracts, database |
| [frontend/README.md](frontend/README.md) | the console and its BFF |
| [capability/README.md](capability/README.md) | the standalone authorisation primitive, and why it has no version stream |
| [docs/configuration.md](docs/configuration.md) | every configuration surface, and the validation run at load |
| [docs/upgrading.md](docs/upgrading.md) | breaking changes between releases |
| [docs/](docs/README.md) | subsystems, [ADRs](docs/adr/) and [runbooks](docs/runbooks/) |
| [CONTRIBUTING.md](.github/CONTRIBUTING.md) | pull requests are not accepted yet; security fixes are |
| [docs/development.md](docs/development.md) | conventions, test tiers, how a pull request is expected to look |
| [SECURITY.md](.github/SECURITY.md) | reporting a vulnerability |
| [docs/security-model.md](docs/security-model.md) | scope, known non-findings, the security model |
| [BACKLOG.md](BACKLOG.md) | deferred work, each item with its reason — read before calling a gap an oversight |

## Layout

| Path | Holds |
|------|-------|
| [backend/](backend) | the Go control plane: binary, proto, migrations, policies, chart, compose stack |
| [frontend/](frontend) | the Next.js console and BFF, its chart and Playwright suite |
| [capability/](capability) | the standalone authorisation module |
| [deploy/](deploy) | Grafana dashboards and alerts |
| [scripts/](scripts) | the repository's own contract checks, run by `verify-all` |
| [specs/](specs) | spec-driven-development artifacts, per feature |
| [docs/](docs) | the documents above |

Both halves share a repository because a wire-contract change is a change to
both: the proto in `proto/` is the source of truth, and the console
regenerates its Connect-ES stubs from it.

Compatibility is not kept across releases yet, and only `main` is supported —
[docs/upgrading.md](docs/upgrading.md) lists what each release breaks.

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
