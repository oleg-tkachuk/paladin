# Operations

Running, building and testing the backend from this directory. The whole stack
from the repository root is in [docs/task.md](../../docs/task.md); installing
on Kubernetes is in [docs/install.md](../../docs/install.md).

## Prerequisites

- Go at the version of the `go` directive in `go.mod`
- Task 3.53+
- Docker with Compose v2 — the compose stack and the integration tests
  (testcontainers) both need it

## Run locally

The compose stack runs every role — api, admin, worker, dispatcher, mcp and
ingest — with PostgreSQL, SeaweedFS and the console:

```bash
task stack:up      # from the repository root
```

Its definition is [`deploy/docker-compose.yaml`](../deploy/docker-compose.yaml),
with `postgres-custom.conf`, `seaweedfs-s3.json` and
`seaweedfs-notification.toml` beside it.

To run one role from a build against your own PostgreSQL and S3:

```bash
task build
./bin/server --config configs/local.yaml serve api
```

`--config` defaults to `/app/configs/config.yaml`, the path the chart mounts
its ConfigMap at. `configs/config.yaml` is the annotated reference for every
key; [configuration.md](configuration.md) explains the loader.

## Migrations

The `migrate` subcommand applies `migrations/` with goose, as the DDL role. In
a cluster the chart runs it as a `pre-install,pre-upgrade` hook Job, so new pods
never start against an unmigrated schema. Rules for new migrations:
[CONVENTIONS.md](../migrations/CONVENTIONS.md).

## Tests

```bash
task test              # unit tests
task test:integration  # Postgres-backed suites; starts containers itself
task test:all          # lint + unit + integration + security
```

Test tiers and where each suite lives: [tests/README.md](../tests/README.md).

## Generated code

```bash
task generate          # mocks and sqlc bindings
task codegen:check     # fail when committed generated output is stale
```

The protobuf stubs are generated into `sdk/go`
(`task -t Taskfile.dev.yaml go-sdk-gen:proto` from the root). sqlc runs at the
version `go.mod` pins.

## Image

[`deploy/Dockerfile`](../deploy/Dockerfile) builds from the repository root (it
copies `capability/` and `sdk/go/`) into a distroless `nonroot` image with the
binary at `/app/bin/paladin-core`. No config is baked in.

```bash
docker build -f backend/deploy/Dockerfile -t paladin-core:dev .   # from the root
```

## Kubernetes

The chart ([`deploy/chart/`](../deploy/chart/)) creates one Deployment and one
Service per enabled role, each running `paladin-core serve <role>`, plus the
migrate and bootstrap Jobs. Liveness, readiness and startup probes are set per
role in `deployments.<role>` in `values.yaml`.

```bash
task deploy        # verify the chart, publish image and chart to the registry
task deploy-local  # build and helm-install into the local cluster, no registry
```

Both need `GLOBAL_REGISTRY` and `IMAGE_NAMESPACE`; the root
`Taskfile.local.yaml` sets them.
