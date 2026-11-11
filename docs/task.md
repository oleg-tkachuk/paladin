# Running Paladin with Task

To install Paladin on a cluster, use the Helm charts. See
[the README](../README.md#deploy-to-kubernetes) and [install.md](install.md).
This page covers the other way in, [Task](https://taskfile.dev). Task runs
Paladin from a clone of the repository: in compose on one machine, on a local
cluster fed from the working tree, or through the gates while you work on the
code.

There are three entry points, split by who runs the command. Each one on its
own prints its handful of commands, and `--list` after any of them prints its
tasks.

| Entry point | For |
|-------------|-----|
| `task` | running the stack in compose |
| `task -t Taskfile.local.yaml` | publishing to a host-only registry and syncing a local ArgoCD |
| `task -t Taskfile.dev.yaml` | the gates, codegen and dependency bumps |

Every flow needs [Task](https://taskfile.dev/installation/) 3.54+. CI pins
3.54.0. The shared [task library](https://github.com/oleg-tkachuk/taskfiles)
is a remote include, cached under `.task/remote/`, so a clone runs without
network access to it. [`.taskrc.yml`](../.taskrc.yml) makes that the default:
every command reads the committed cache and fetches nothing. See
[Moving the task library](#moving-the-task-library) for the one command that
goes online.

## The whole stack in compose

### Prerequisites

| Tool | Why |
|------|-----|
| [Docker](https://docs.docker.com/get-started/get-docker/) with Compose v2 | every plane, Postgres, SeaweedFS and the console run as containers |
| Task 3.54+ | the entry point |

That is all. The stack needs no cluster, no credentials and no toolchain. If
`task stack:up` asks you for any of them, that is a bug.

### Run it

```bash
git clone https://github.com/oleg-tkachuk/paladin.git && cd paladin
task stack:up        # every plane + Postgres + SeaweedFS + the console, in compose
```

`stack:up` waits until every service is healthy. The console is on
<http://localhost:3002> and the data plane on `:8083` (not `:8080`, which
another local service uses). Admin is on `:8090`. The compose file,
[`backend/deploy/docker-compose.yaml`](../backend/deploy/docker-compose.yaml),
lists the rest.

```bash
task stack:down      # stop, keep volumes
task stack:reset     # stop and delete volumes
```

## A local cluster, fed from the working tree

`Taskfile.local.yaml` is the loop between releases. It builds both images and
both charts from the working tree, publishes them to a registry the cluster
pulls from, and has ArgoCD pick them up.

### Prerequisites

| Needs | Default, and how to change it |
|-------|-------------------------------|
| Docker with buildx | builds both images |
| A Kubernetes cluster, and `kubectl` with a context for it | context `orbstack`; override with `K8S_CONTEXT=<context>` |
| An OCI registry that both the host and the cluster reach | `registry.local`; override with `GLOBAL_REGISTRY=<host>` and `IMAGE_NAMESPACE=<path>` |
| [Helm](https://helm.sh/docs/intro/install/) 3.8+ | packages and pushes both charts |
| ArgoCD in the cluster, with the `argocd` CLI | Applications named `paladin-core…` and `paladin-console…`, tracking the charts in that registry |
| PostgreSQL and an object store the release can reach | as for any install; see [install.md](install.md) |

The ArgoCD Applications are not part of this repository. Point them at
`oci://<registry>/<namespace>/charts` with the values from
[install.md](install.md), and see its [GitOps](install.md#gitops) section for
what to provide when ArgoCD renders the charts.

### Run it

```bash
task -t Taskfile.local.yaml deploy        # build and publish both images and charts
task -t Taskfile.local.yaml deploy:sync   # hard-refresh the ArgoCD applications
```

`deploy` fails fast when the registry does not answer.
`task -t Taskfile.local.yaml backend:deploy` and `frontend:deploy` publish one
component at a time.

## Working on the code

### Prerequisites

| Tool | Why |
|------|-----|
| [Docker](https://docs.docker.com/get-started/get-docker/) | testcontainers, `verify-deep` and `verify-e2e` |
| Task 3.54+ | every gate is a task |
| [Go](https://go.dev/dl/) 1.27+ | `backend/` and `sdk/go/`, per their `go.mod`; the toolchain auto-downloads |
| [Node](https://nodejs.org) 26 | the console; the version its image ships and CI verifies with |
| [pnpm](https://pnpm.io) | pinned by `packageManager` in `frontend/package.json`; Node 26 has no corepack, so `npm install -g pnpm@<that version>` |

`verify-all` also shells out to golangci-lint, buf, helm, yq and python3. On
macOS, `brew bundle` installs that set. The [Brewfile](../Brewfile) says which
tools are pinned elsewhere instead, and why.
[development.md](development.md) covers the optional git hooks and the
conventions they enforce.

### Run it

| Task | Does |
|------|------|
| `task -t Taskfile.dev.yaml verify-all` | the commit gate: every tree's tests and lint, the proto compatibility check, the chart and Taskfile contract checks, the console build; no Docker |
| `task -t Taskfile.dev.yaml verify-deep` | the Postgres-backed integration suites and the gates that need a live stack; Docker, ~3 min |
| `task -t Taskfile.dev.yaml verify-e2e` | Playwright against images built from the current branch; Docker, ~10 min |

Per half, from any entry point: `task backend:test`,
`task backend:test:integration`, `task frontend:test`, `task frontend:build`.

### Component tasks

`backend/` and `frontend/` each have a Taskfile of their own. An entry point
does not include them: `task backend:<task>` starts `task <task>` in
`backend/` as a separate process, and the same for `frontend`. Each component
therefore reads only its own variables, and a few things follow:

- `--list` shows one `backend:*` and one `frontend:*` line. `task
  backend:default` and `task frontend:default` list a component's tasks.
- Of the variables given on the command line, `GLOBAL_REGISTRY`,
  `IMAGE_NAMESPACE`, `K8S_CONTEXT` and `APP_VERSION` reach the component. Put
  any other in front of the command, `NAME=value task backend:<task>`. One the
  component's Taskfile declares itself, such as `K8S_NAMESPACE`, is overridden
  only from the component's directory: `cd backend && task <task> NAME=value`.
- Arguments after `--` reach the component's task unchanged.
- Flags such as `--dry` and `--force` apply to the entry point, not to the
  component's task. Use them from the component's directory.

### Moving the task library

All five Taskfiles pin the library at one tag, and each directory that holds
one keeps the modules it includes under `.task/remote/`. One command moves the
pins and replaces the caches:

```bash
task -t Taskfile.dev.yaml tasklib:bump VERSION=v14.6.2
```

Renovate proposes the same bump, but moves only the pins. On its branch
`task -t Taskfile.dev.yaml tasklib:check`, part of `verify-all`, fails until
the caches are refreshed and committed:

```bash
task -t Taskfile.dev.yaml --yes --offline=false --download tasklib:lock
```

`--yes` accepts what the new tag serves, so read the diff of `.task/remote/`
as you would a dependency bump.
