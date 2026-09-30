# paladin

[![ci](https://github.com/oleg-tkachuk/paladin/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/oleg-tkachuk/paladin/actions/workflows/ci.yaml)
[![release](https://img.shields.io/github/v/release/oleg-tkachuk/paladin?sort=semver)](https://github.com/oleg-tkachuk/paladin/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/oleg-tkachuk/paladin?filename=backend%2Fgo.mod&logo=go&logoColor=white)](backend/go.mod)
[![license](https://img.shields.io/github/license/oleg-tkachuk/paladin)](LICENSE)

[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org)
[![Connect RPC](https://img.shields.io/badge/Connect%20RPC-1D4ED8)](https://connectrpc.com)
[![Cedar](https://img.shields.io/badge/Cedar-FF9900)](https://www.cedarpolicy.com)
[![MCP](https://img.shields.io/badge/MCP-000000?logo=modelcontextprotocol&logoColor=white)](https://modelcontextprotocol.io)
[![Next.js](https://img.shields.io/badge/Next.js-000000?logo=nextdotjs&logoColor=white)](https://nextjs.org)

A multi-tenant control plane for S3-compatible object storage. Applications
authenticate with a Paladin credential scoped to a tenant instead of holding
storage credentials; Paladin checks each request against the tenant's Cedar
policies, routes it to the backend the tenant is on, meters usage against
quotas, and writes an audit trail.

Access is granted through users, API tokens and capability tokens —
short-lived, budgeted and individually revocable. The capability primitive is
[`capability/`](capability/), a separate Go module with no database driver
and no storage SDK among its dependencies. Besides the Connect API, the
console and the Go and Python SDKs, the same operations are exposed over the
Model Context Protocol, so AI agents can use them as tools.

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
proto/        the API contract, read by backend, console and both SDKs
sdk/go/       Go SDK: generated Connect clients + a thin client
sdk/python/   Python SDK: the same, on connect-python
```

## Contents

- [Deploy to Kubernetes](#deploy-to-kubernetes) — the Helm charts, on your cluster
- [Run it with Task](#run-it-with-task) — from a clone: compose, a local cluster, the gates
- [How changes land](#how-changes-land) — trunk, CI and releases
- [Documentation](#documentation) — the rest, by document
- [Layout](#layout) — where things live in the tree
- [License](#license)

## Deploy to Kubernetes

Two Helm charts, published to GHCR with every release (`vX.Y.Z`):

| Chart | Runs |
|-------|------|
| `oci://ghcr.io/oleg-tkachuk/charts/paladin-core` | the backend planes, plus the migrate and bootstrap Jobs |
| `oci://ghcr.io/oleg-tkachuk/charts/paladin-console` | the web console and its BFF |

The charts run neither PostgreSQL nor the object store: bring PostgreSQL 16+
and an S3-compatible store whose access key may create buckets. Kubernetes
1.25+ and Helm 3.8+.

1. Create the three database roles, once, as a superuser — the SQL is in
   [docs/install.md](docs/install.md#1-database-roles).
2. Put the credentials into Secrets:

   ```bash
   kubectl create namespace paladin
   kubectl -n paladin create secret generic paladin-postgres-app --from-literal=password='<app-password>'
   kubectl -n paladin create secret generic paladin-postgres-migrate --from-literal=password='<migrate-password>'
   kubectl -n paladin create secret generic paladin-s3 \
     --from-literal=access_key='<access-key>' --from-literal=secret_key='<secret-key>'
   ```

3. Give the backend chart what it cannot default:

   ```yaml
   # paladin-core.yaml
   postgres:
     host: postgres.databases.svc.cluster.local
     sslmode: require
     app:
       existingSecret: paladin-postgres-app
     migrate:
       existingSecret: paladin-postgres-migrate
   config:
     storage:
       backends:
         primary:
           endpoint: http://seaweedfs.storage.svc.cluster.local:8333
           region: us-east-1
   storage:
     s3CredentialsSecret:
       create: false
       existingSecret: paladin-s3
   ```

4. Install both charts:

   ```bash
   helm install paladin-core oci://ghcr.io/oleg-tkachuk/charts/paladin-core \
     --version <version> -n paladin -f paladin-core.yaml
   helm install paladin-console oci://ghcr.io/oleg-tkachuk/charts/paladin-console \
     --version <version> -n paladin
   ```

5. Sign in as `admin`, with the password generated on first install:

   ```bash
   kubectl -n paladin get secret paladin-bootstrap-admin -o jsonpath='{.data.password}' | base64 -d
   kubectl -n paladin port-forward svc/paladin-console 3000:3000
   ```

[docs/install.md](docs/install.md) covers the rest: ingress, TLS between the
planes, and what to set when ArgoCD renders the charts.

## Run it with Task

Paladin also runs from a clone of the repository through
[Task](https://taskfile.dev): the whole stack in compose with nothing but
Docker (`task stack:up`), a local cluster fed from the working tree, or the
gates while you work on the code. [docs/task.md](docs/task.md) covers each
flow and what it needs installed.

## How changes land

`main` is the only long-lived branch. Changes reach it through pull requests,
with [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).

[`ci.yaml`](.github/workflows/ci.yaml) runs `verify-all` and audits the
workflows with actionlint and zizmor. A green push to `main` dispatches
[`release.yaml`](.github/workflows/release.yaml): semantic-release computes
the next tag from the commits since the last one, a GitHub release with
generated notes is created for it, and both images and charts are pushed to
GHCR at that version. The SDK and API-contract tags follow their own stream —
see [docs/releasing.md](docs/releasing.md).

`ci.yaml` builds no image and deploys nothing; images and charts are published
only by `release.yaml`, for a release tag. `verify-deep` and
`verify-e2e` need Docker and run locally, before a merge.

## Documentation

| Document | Covers |
|----------|--------|
| [ARCHITECTURE.md](ARCHITECTURE.md) | what the pieces are and why the boundaries fall where they do |
| [backend/README.md](backend/README.md) | roles, ports, packages, wire contracts, database |
| [frontend/README.md](frontend/README.md) | the console and its BFF |
| [capability/README.md](capability/README.md) | the standalone authorisation primitive, and why it has no version stream |
| [docs/install.md](docs/install.md) | installing on Kubernetes with the Helm charts |
| [docs/task.md](docs/task.md) | running it with Task — compose, a local cluster, the gates — and the prerequisites of each |
| [docs/configuration.md](docs/configuration.md) | every configuration surface, and the validation run at load |
| [docs/upgrading.md](docs/upgrading.md) | breaking changes between releases |
| [docs/releasing.md](docs/releasing.md) | what each tag family publishes and who cuts it |
| [docs/](docs/README.md) | subsystems, [ADRs](docs/adr/) and [runbooks](docs/runbooks/) |
| [CONTRIBUTING.md](.github/CONTRIBUTING.md) | pull requests are not accepted yet; security fixes are |
| [docs/development.md](docs/development.md) | conventions, test tiers, how a pull request is expected to look |
| [SECURITY.md](.github/SECURITY.md) | reporting a vulnerability |
| [docs/security-model.md](docs/security-model.md) | scope, known non-findings, the security model |
| [BACKLOG.md](BACKLOG.md) | deferred work, each item with its reason — read before calling a gap an oversight |

## Layout

| Path | Holds |
|------|-------|
| [backend/](backend) | the Go control plane: binary, migrations, policies, chart, compose stack |
| [frontend/](frontend) | the Next.js console and BFF, its chart and Playwright suite |
| [capability/](capability) | the standalone authorisation module |
| [proto/](proto) | the API contract |
| [sdk/go/](sdk/go) | the Go SDK |
| [sdk/python/](sdk/python) | the Python SDK |
| [deploy/](deploy) | Grafana dashboards and alerts |
| [scripts/](scripts) | the repository's own contract checks, run by `verify-all` |
| [specs/](specs) | spec-driven-development artifacts, per feature |
| [docs/](docs) | the documents above |

The components share a repository because a wire-contract change is a change
to all of them: the proto in `proto/` is the source of truth, and the backend,
the console and both SDKs generate their stubs from it.

Compatibility is not kept across releases yet, and only `main` is supported —
[docs/upgrading.md](docs/upgrading.md) lists what each release breaks.

## License

Apache-2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
