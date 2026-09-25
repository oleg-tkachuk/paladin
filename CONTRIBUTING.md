# Contributing

Thanks for looking. This document is the short version of how the project
actually works — the conventions here are enforced by hooks and CI, so
knowing them up front saves a round trip.

If you are reporting a security problem, stop and read
[SECURITY.md](SECURITY.md) instead.

## Prerequisites

| Tool | Version | Why |
| --- | --- | --- |
| Go | 1.27+ | the `go` directive in `backend/go.mod` and `capability/go.mod`; the toolchain auto-downloads |
| Node | 26 | matches `frontend/deploy/Dockerfile`; CI runs the same |
| pnpm | 12.7.0 | pinned by `packageManager` in `frontend/package.json`; Node 26 ships no corepack, so `npm install -g pnpm@12.7.0` |
| Docker | recent | compose stacks, testcontainers-backed integration tests |
| [Task](https://taskfile.dev) | 3.53+ | every entry point is a task target; CI pins 3.53.1 |

Optional but recommended — the git hooks call these and **silently skip
when they are missing**, so a fresh clone can commit, but you lose the
local check:

```bash
brew install lefthook gitleaks            # hooks + secret scanning
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
lefthook install                          # wire the hooks into .git/hooks
```

## Getting a stack up

```bash
task stack:up          # backend + console + Postgres + SeaweedFS, in compose
task verify-all      # build + test both halves — the fast, pre-commit gate
task verify-deep     # the slow one: integration suites + live-stack gates
task verify-e2e      # Playwright, against images built from your branch
task --list-all      # everything, across both namespaces
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
| E2E | `task verify-e2e` | Docker (rebuilds both images, then Playwright boots the compose stack) |
| Capability module | `task verify-capability` (part of `task verify-all`) | nothing, deliberately |

The capability module must resolve **no** database driver and **no**
object-storage SDK in its dependency graph, so that a third party can use it
without either. `isolation_test.go` in the module asserts this against the
resolved graph, so `task verify-capability` — and with it `verify-all` and CI
— fails if your change pulls either in. The right fix is almost always to
move the code into `backend/` instead.

New behaviour needs a test. The project constitution is explicit that the
middleware and store layers must be covered — that is where three-valued
logic bugs hide, and there is a NULL-cursor regression in the history that
lived for weeks to prove it.

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
  have been a `fix:` publishes a minor release.
- **One scope per commit.** A commit that touches auth and the console and
  the chart is three commits. This is a constitution principle, not a
  preference — it is what makes `git log` a usable audit trail for a
  system whose whole subject is authorisation.

## BACKLOG.md

[BACKLOG.md](BACKLOG.md) is the single source of truth for deferred work.
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
[`docs/adr/`](docs/adr/). Follow the numbering and the existing format;
[`docs/adr/README.md`](docs/adr/README.md) explains it.

Adding a dependency under a copyleft or source-available licence requires
an ADR. Everything currently in the tree is Apache-2.0, MIT or BSD.

## Spec-driven development

Non-trivial features flow through the Spec Kit workflow
(`/speckit-specify` → `/speckit-plan` → `/speckit-tasks` →
`/speckit-implement`), and the resulting artifacts live in
[`specs/`](specs/). Every plan passes the seven-principle Constitution
Check in [`.specify/memory/constitution.md`](.specify/memory/constitution.md)
before implementation.

You do **not** need to use this workflow to contribute. A bug fix, a
dependency bump, a doc correction or a small feature is welcome as a plain
pull request. The machinery exists because much of this codebase was built
with AI assistance and the specs are how that work stays reviewable — if
you are proposing something large, reading the relevant `specs/` directory
first will tell you what was already considered and rejected.

The `.agents/`, `.claude/`, `.specify/` and `.rtk/` directories are that
tooling. They are committed on purpose, and you can ignore all of them.

## Pull requests

- Branch from `main` and open the pull request against `main`. There is no
  other long-lived branch.
- Make sure `task verify-all` passes before you push. CI
  (`.github/workflows/ci.yaml`) runs exactly that task, plus actionlint and
  zizmor over the workflows, so a red pipeline usually means a step was
  skipped locally because the tool was not installed.
- Run `task verify-deep` before you ask for a merge. It needs Docker and
  takes about fifteen minutes, which is why it is not the pre-commit gate —
  but it is the only thing that runs the integration suites, the whole-contract
  RPC gate, the Go admin e2e suite, the S3 conformance suite and
  `dev-bootstrap.sh` against a stack built from your branch. Every one of those
  has silently rotted at least once while `verify-all` stayed green; a compile
  check cannot catch a suite that builds and then fails.
- Run `task verify-e2e` too if you touched the console or a plane's wire
  format. Prefer it over a bare `pnpm run test:e2e`: that rebuilds nothing, and
  the stack runs `:latest`, so the result describes whichever images happen to
  be on the machine. This repository has twice read a green run as verifying a
  commit the images did not contain.
- Keep the diff to one concern. If review surfaces a second one, a
  follow-up PR is better than growing this one.
- Explain *why* in the description. The what is in the diff.

Contributions are accepted under the Apache License 2.0 (see LICENSE §5).
There is no CLA.

## Getting help

Open an issue with the question label, or start a discussion. Questions
about whether something is a bug or a deliberate design choice are
welcome — the answer is often in BACKLOG.md or an ADR, and if it is not,
that is a documentation bug worth fixing.
