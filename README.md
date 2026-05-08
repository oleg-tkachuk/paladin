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
  services AND mounts frontend as the `paladin-ui` container with a
  build context resolving to `../../frontend`. The integrated stack
  starts via `task e2e-up`.
- **Helm**: backend chart at `backend/deploy/chart/`; frontend chart
  at `frontend/deploy/chart/`. Each is independently installable.
