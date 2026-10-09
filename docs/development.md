# Development guide

How the project works — the conventions here are enforced by hooks and CI,
so knowing them up front saves a round trip.

The repository does not accept pull requests yet; see
[CONTRIBUTING.md](../.github/CONTRIBUTING.md). If you are reporting a security
problem, read [SECURITY.md](../.github/SECURITY.md) instead.

## Prerequisites

| Tool | Version | Why |
| --- | --- | --- |
| Go | 1.27+ | the `go` directive in `backend/go.mod` and `capability/go.mod`; the toolchain auto-downloads |
| Node | 26 | matches `frontend/deploy/Dockerfile`; CI runs the same |
| pnpm | 12.8.2 | pinned by `packageManager` in `frontend/package.json`; Node 26 ships no corepack, so `npm install -g pnpm@12.8.2` |
| Docker | recent | compose stacks, testcontainers-backed integration tests |
| [Task](https://taskfile.dev) | 3.53+ | every entry point is a task target; CI pins 3.53.1 |

Optional but recommended — the git hooks call these and **silently skip
when they are missing**, so a fresh clone can commit, but you lose the
local check:

```bash
brew install lefthook gitleaks            # hooks + secret scanning
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0   # GOLANGCI_LINT_VERSION in ci.yaml
lefthook install                          # wire the hooks into .git/hooks
```

## Getting a stack up

```bash
task stack:up   # backend + console + Postgres + SeaweedFS, in compose
task            # the rest of the commands for operating it
```

The gates live behind the second entry point — `task` is what an operator
runs, and these are not their commands:

```bash
task -t Taskfile.dev.yaml               # its own list of commands
task -t Taskfile.dev.yaml verify-all    # the pre-commit gate: build + test
task -t Taskfile.dev.yaml verify-deep   # integration suites + live-stack gates
task -t Taskfile.dev.yaml verify-e2e    # Playwright, on images from your branch
task -t Taskfile.dev.yaml --list        # everything it reaches
```

`task stack:up` needs nothing but Docker. If it asks you for a credential or
a cluster, that is a bug — please report it.

Per-half loops:

```bash
task backend:build            task backend:test
task frontend:node:typecheck  task frontend:build
```

## Tests

Four tiers, and they run in different places for a reason:

| Tier | Command | Needs |
| --- | --- | --- |
| Unit | `task backend:test` / `cd frontend && pnpm test` | nothing |
| Integration | `task backend:test:integration` | Docker (testcontainers spins a real Postgres) |
| E2E | `task -t Taskfile.dev.yaml verify-e2e` | Docker (rebuilds both images, then Playwright boots the compose stack) |
| Capability module | `task -t Taskfile.dev.yaml verify-capability` (part of `verify-all`) | nothing, deliberately |

The capability module must resolve **no** database driver and **no**
object-storage SDK in its dependency graph, so that a third party can use it
without either. `isolation_test.go` in the module asserts this against the
resolved graph, so `verify-capability` — and with it `verify-all` and CI
— fails if your change pulls either in. The right fix is almost always to
move the code into `backend/` instead.

New behaviour needs a test, in the same commit. The middleware and store
layers especially: that is where three-valued logic bugs hide, and there is a
NULL-cursor regression in the history that lived for weeks to prove it.

## Commits

Conventional Commits, enforced by a `commit-msg` hook:

```
<type>(<scope>): <subject>

feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert|security
```

Two things follow from this that are easy to miss:

- **The type drives releases.** Once CI passes on a push to `main`, it
  dispatches `.github/workflows/release.yaml`, where `semantic-release` reads
  the history and computes the next version from it. A `feat:` that should
  have been a `fix:` publishes a minor release. `fix`, `perf`, `revert` and
  `security` publish a patch; `docs`, `ci`, `chore`, `refactor`, `test`,
  `build` and `style` publish nothing on their own.
- **One scope per commit.** A commit that touches auth and the console and
  the chart is three commits. It is what makes `git log` a usable audit
  trail for a system whose whole subject is authorisation.

## BACKLOG.md

[BACKLOG.md](../BACKLOG.md) is the single source of truth for deferred work.
It is long, and that is the point: everything this project decided *not*
to do is written down with the reason.

The rules, in full:

- Deciding not to implement something — scope cut, missing metric,
  blocking question, design uncertainty — means an entry lands **in the
  same commit** that creates or surfaces the gap.
- Every entry carries **Status / Reason / Definition of Done / Blockers**.
- Completing an item means **deleting its entry**, in the commit that
  completes it. No "✅ done" markers; git history is the audit trail.
- A `TODO` or `FIXME` in code that has no BACKLOG entry is drift. Resolve
  it or migrate it.

If your PR leaves something unfinished on purpose, say so in BACKLOG.md
and the reviewer will treat it as a decision rather than an oversight.

## Architecture decisions

Anything that changes a boundary — a new dependency direction, a new
trust relationship, a storage or transport choice — wants an ADR in
[`docs/adr/`](adr/). Follow the numbering and the existing format;
[`docs/adr/README.md`](adr/README.md) explains it.

Adding a dependency under a copyleft or source-available licence requires
an ADR. Every dependency currently in the tree is Apache-2.0, MIT or BSD.

## Spec-driven development

Larger features are specified before they are built, and the resulting
artifacts live in [`specs/`](../specs/). If you are proposing something
large, read the relevant `specs/` directory first: it records what was
already considered and rejected. A bug fix, a dependency bump, a doc
correction or a small feature needs none of this.

## Pull requests

- Branch from `main` and open the pull request against `main`. There is no
  other long-lived branch.
- Make sure `task -t Taskfile.dev.yaml verify-all` passes before you push. CI
  (`.github/workflows/ci.yaml`) runs the groups of that task a change can
  reach, plus actionlint and zizmor over the workflows, so a red pipeline
  usually means a step was skipped locally because the tool was not installed.
  A change to documentation alone runs none of them.
- Run `task -t Taskfile.dev.yaml verify-deep` before you ask for a merge. It
  needs Docker and takes about fifteen minutes, which is why it is not the
  pre-commit gate — but it is the only thing that runs the integration suites,
  the whole-contract RPC gate, the Go admin e2e suite, the S3 conformance suite,
  `dev-bootstrap.sh` and both SDKs' conformance scenarios against a stack built
  from your branch. Every one of those
  has silently rotted at least once while `verify-all` stayed green; a compile
  check cannot catch a suite that builds and then fails.
- Run `task -t Taskfile.dev.yaml verify-e2e` too if you touched the console or
  a plane's wire format. Prefer it over a bare `pnpm run test:e2e`: that
  rebuilds nothing, and the stack runs `:latest`, so the result describes
  whichever images happen to be on the machine. This repository has twice read
  a green run as verifying a commit the images did not contain.
- Keep the diff to one concern. If review surfaces a second one, a
  follow-up PR is better than growing this one.
- Explain *why* in the description. The what is in the diff.

Contributions are accepted under the licence of the directory they touch:
AGPL-3.0-only, or Apache-2.0 under `proto/`, `sdk/` and `capability/` (see
[NOTICE](../NOTICE) and [ADR-0030](adr/0030-agpl-with-apache-client-surface.md)).
There is no CLA.

## Getting help

Open an issue with the question label, or start a discussion. Questions
about whether something is a bug or a deliberate design choice are
welcome — the answer is often in BACKLOG.md or an ADR, and if it is not,
that is a documentation bug worth fixing.
