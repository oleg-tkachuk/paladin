# Paladin (PALADIN)

Monorepo: Go control-plane backend + Next.js admin UI.

## Layout

```
.
├── backend/        Go service (cmd/, internal/, migrations/, proto/, …)
├── frontend/       Next.js BFF + admin UI (src/, deploy/, …)
├── tasks/          Cross-project Taskfiles (compose, e2e meta-tasks)
├── Taskfile.yaml   Top-level orchestrator — `task --list-all`
└── BACKLOG.md      Deferred work register (see CLAUDE.md for the policy)
```

The two halves live in one repo so the proto schema, Helm chart,
and integration tests can move together.

## Quick start

```bash
# Backend + UI in one compose stack:
task e2e-up

# Verify both halves build + test:
task verify-all

# Backend-only loop:
task backend:build
task backend:test

# Frontend-only loop:
task frontend:tsc
task frontend:build
```

`task --list-all` enumerates everything across both namespaces.

### Optional local hook tools

Git hooks run via [lefthook](https://github.com/evilmartians/lefthook). Some
hook commands **no-op silently when their tool isn't on `PATH`** so a fresh
clone can still commit/push — CI is the backstop, but install these to catch
issues locally first:

```bash
# Secret scanning (lefthook pre-commit `gitleaks`; CI runs it regardless):
brew install gitleaks            # or: go install github.com/gitleaks/gitleaks/v8@latest
# Go import grouping + lint (pre-commit/pre-push):
go install golang.org/x/tools/cmd/goimports@latest
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
```

## Per-project entry points

- Backend — see [backend/README.md](backend/README.md) for Go module
  layout, migrations, Connect-RPC service map.
- Frontend — see [frontend/README.md](frontend/README.md) for Next.js
  BFF and admin UI structure.

## Cross-project artifacts

- **Proto**: source of truth in `backend/proto/`. Frontend regenerates
  Connect-ES stubs via `cd frontend && npm run generate` (reads
  `../backend/proto`).
- **Compose**: `backend/deploy/docker-compose.yaml` builds backend
  services AND mounts frontend as the `paladin-console` container with a
  build context resolving to `../../frontend`. The integrated stack
  starts via `task e2e-up`.
- **Helm**: backend chart at `backend/deploy/chart/`; frontend chart
  at `frontend/deploy/chart/`. Each is independently installable.
