# Data Model — Frontend Playwright E2E

This feature ships **test infrastructure**, not production
data models. The entities below describe the runtime objects
the test stack and fixture helpers materialise — they're not
proto schemas or SQL migrations. The "data" of this feature
is the test-stack's runtime topology + the per-test seed
state.

## Entity: Test Stack

The composed runtime that supports a Playwright run.
Materialised by `docker-compose.test.yaml`.

| Field | Type | Value | Notes |
|---|---|---|---|
| `compose_project` | string | `paladin-e2e` | Isolates the test stack's containers/volumes from the dev-loop stack in `backend/deploy/docker-compose.yaml`. |
| `network` | string | `paladin-e2e-net` | Project-scoped Docker network. |
| `services` | service[] | see below | Seven services: `postgres`, `migrate`, `bootstrap`, `api`, `admin`, `garage`, `ui`. |
| `lifecycle` | enum | `up → ready → tests-run → down` | `up`/`down` via `docker compose -p paladin-e2e -f frontend/tests/e2e/docker-compose.test.yaml up/down`. `ready` is when all services pass their healthchecks. |
| `cleanup_policy` | enum | `volumes-removed-on-down` | Ephemeral. Every CI/local run starts from zero. |

### Service: `postgres`

| Field | Value |
|---|---|
| Image | `postgres:16` |
| Healthcheck | `pg_isready -U paladin` |
| Env | `POSTGRES_USER=paladin`, `POSTGRES_PASSWORD=paladin-e2e`, `POSTGRES_DB=paladin` |
| Volume | tmpfs (not persisted between runs) |

### Service: `migrate` (one-shot)

| Field | Value |
|---|---|
| Image | `registry.local/paladin/paladin:latest` |
| Command | `migrate up` |
| Depends on | `postgres` healthy |
| Termination | success required — `condition: service_completed_successfully` |

### Service: `bootstrap` (one-shot)

| Field | Value |
|---|---|
| Image | Same as `migrate` |
| Command | `bootstrap` |
| Env | `PALADIN_BOOTSTRAP_ADMIN_SUBJECT=e2e-admin@local`, `PALADIN_BOOTSTRAP_ADMIN_PASSWORD=e2e-not-a-secret-2026` |
| Depends on | `migrate` success |

### Service: `api`

| Field | Value |
|---|---|
| Image | Same as `migrate` |
| Command | `serve api` |
| Ports (host:container) | `8080:8080` (data plane), `8085:8085` (iam plane) |
| Healthcheck | `wget -qO- http://localhost:8080/system/health.json` |
| Depends on | `bootstrap` success |

### Service: `admin`

| Field | Value |
|---|---|
| Image | Same as `migrate` |
| Command | `serve admin` |
| Ports | `8090:8090` |
| Healthcheck | `/system/health.json` on `:8090` |
| Depends on | `bootstrap` success |

### Service: `garage`

| Field | Value |
|---|---|
| Image | `dxflrs/garage:v2.3.0` |
| Command | `garage server --single-node --default-bucket paladin-e2e` |
| Env | `GARAGE_DEFAULT_ACCESS_KEY=e2e-key`, `GARAGE_DEFAULT_SECRET_KEY=e2e-secret`, `GARAGE_DEFAULT_BUCKET=paladin-e2e` |
| Ports | `3900:3900` (S3 API), `3902:3902` (admin) |
| Healthcheck | `curl -f http://localhost:3902/health` |

### Service: `ui`

| Field | Value |
|---|---|
| Build context | `frontend/` |
| Command | `next start` (production-mode bundle for stable behaviour) |
| Ports | `3000:3000` |
| Env | `NEXT_PUBLIC_API_BASE_URL=http://api:8080`, `NEXT_PUBLIC_IAM_BASE_URL=http://api:8085`, `NEXT_PUBLIC_ADMIN_BASE_URL=http://admin:8090` |
| Depends on | `api` healthy + `admin` healthy |

**State transitions**: `created → starting → healthy → tests-running → done`. Each service exposes a binary healthy/unhealthy via its healthcheck; Playwright's `webServer` config in `playwright.config.ts` waits for the entire stack to be healthy before opening the first page.

## Entity: Seeded Admin

Static fixture user provisioned by the `bootstrap` service.

| Field | Type | Value |
|---|---|---|
| `subject` | string | `e2e-admin@local` |
| `password` | string | `e2e-not-a-secret-2026` (NEVER true in prod — fixture credential only) |
| `tenant_id` | UUID | NULL (platform-admin operates cross-tenant) |
| `roles` | string[] | `["platform-admin"]` |
| `created_at` | timestamptz | bootstrap time |

The seeded admin is **read-only across tests** — no test
modifies it. Tests log in via the real `/login` form using
these credentials and then create per-test tenants/buckets.

## Entity: Per-Test Tenant Fixture

Created in `test.beforeEach` via `seedTenant()`. One per
test; reused across pages within the same test.

| Field | Type | Value | Notes |
|---|---|---|---|
| `tenant_id` | UUID | server-generated via CreateTenant | Returned in the Connect-RPC response |
| `slug` | string | `acme-${randomHex(8)}` | UUID-suffixed; satisfies the UNIQUE constraint from migration 033 |
| `display_name` | string | `Acme E2E ${randomHex(8)}` | UUID-suffixed |
| `cedar_inherited` | string | `""` | Default — tests override per-story when needed |
| `lifetime` | enum | `created → mutated → ephemeral-via-compose-down` | No explicit teardown; the next `docker compose down` wipes the DB volume |

## Entity: Per-Test Resources (US-specific)

Each user story seeds additional fixtures beyond the tenant:

| US | Resource | Seeded by | Cleanup |
|---|---|---|---|
| US1 | (none — uses only the seeded admin) | n/a | n/a |
| US2 | A second `Globex-${suffix}` tenant | `seedTenant()` (called twice) | compose-down |
| US3 | One bucket + one ObjectKey under the test tenant | `seedBucket()`, `seedObjectKey()` | compose-down |
| US4 | (none — capability is the artefact under test) | tests create via UI | compose-down |
| US5 | (none — tenant itself is the artefact) | tests soft-delete via UI | compose-down |

## Entity: Failure Artifact Bundle

Produced by Playwright on test failure. Directory layout:

```
frontend/tests/e2e/test-results/
└── <test-name>-<browser>-<retry>/
    ├── test-failed-1.png        # final screenshot
    ├── video.webm               # full-test recording (failure only)
    └── trace.zip                # Playwright trace — DOM + network + console
```

Local-only in v1 — see FR-006. The `.gitignore` MUST exclude
this directory.
