# Paladin Frontend E2E (Playwright)

Operator-facing UI regression guards for the Paladin admin UI. 15
test scenarios across 5 user stories. Design rationale + the
SDD audit trail live at
[`specs/001-frontend-playwright-e2e/`](../../../specs/001-frontend-playwright-e2e/).

## Quick start

```bash
# Prerequisites (one-time):
#   - Docker daemon running
#   - Paladin backend + UI images present locally as
#     registry.local/paladin/paladin-core:latest
#     registry.local/paladin/paladin-console:latest
#     (build via `task -d backend build:image` if missing)
#   - pnpm 11.3.0+: `corepack enable && corepack prepare pnpm@11.3.0 --activate`
#   - Chromium binary: `pnpm exec playwright install chromium`

cd frontend

# Run the suite. Playwright's webServer config brings up the
# docker-compose test stack — Postgres, MinIO and the Paladin planes —
# and tears it down on exit.
pnpm run test:e2e
```

There is nothing else to arrange: no cluster, no port-forward, no
credential export.

## Storage

MinIO runs as a service inside `docker-compose.test.yaml`, with a
one-shot `minio-setup` container that creates the bucket before the
API plane starts. Credentials are the committed dev pair
(`paladin-e2e-access` / `paladin-e2e-secret-key`) — dev-only, and listed in the
backend's weak-secret deny-list so no real deployment can inherit them.

Two endpoints are configured and they are not interchangeable:
`endpoint` (`http://minio:9000`) is what the backend talks to over
container DNS, and `public_endpoint` (`http://localhost:9000`) is what
presigned URLs are signed for, because the browser uploads from the
host. SigV4 covers the Host header, so swapping them produces a
signature failure rather than a connection error.

To run against an external S3 endpoint instead — Garage, SeaweedFS, a
real bucket — set all five variables before `pnpm run test:e2e`:

```bash
export PALADIN_E2E_S3_ENDPOINT=https://s3.example.com
export PALADIN_E2E_S3_PUBLIC_ENDPOINT=https://s3.example.com
export PALADIN_E2E_S3_BUCKET=paladin-e2e
export PALADIN_E2E_S3_ACCESS_KEY=…
export PALADIN_E2E_S3_SECRET_KEY=…
```

## Layout

```
tests/e2e/
├── fixtures/
│   ├── credentials.ts     # seeded admin user (NEVER-in-prod marker)
│   ├── unique.ts          # uniqueSlug() — UUID-suffixed identifiers
│   ├── auth.ts            # loginAsAdmin(), logout() — UI-driven
│   └── seed.ts            # seedTenant, seedBucket, seedCollection,
│                          # seedCapability — Connect-RPC direct
├── environment.setup.ts   # runs first, gates the rest — see below
├── auth.spec.ts           # US1 — login + AuthGate redirect (4 tests)
├── scope.spec.ts          # US2 — scope picker behavior (3 tests)
├── buckets.spec.ts        # US3 — bucket → Collection navigation (3 tests)
├── capabilities.spec.ts   # US4 — capability lifecycle + FR-008 (3 tests)
├── trash.spec.ts          # US5 — tenant restore from trash (2 tests)
└── docker-compose.test.yaml   # 6 services: postgres, migrate,
                                 # bootstrap, api, admin, ui
```

## The environment gate

`environment.setup.ts` is a Playwright project the `chromium` project depends
on, so it runs before any browser starts and stops the run if it fails.

It answers one question: is this stack already full of fixtures nobody cleaned
up? Teardown removes what each test creates, but a run that is aborted
half-way leaves rows behind, and they accumulate silently. At roughly a
hundred leftover tenants the suite starts failing — as a scope assertion
matching the wrong "Switch Target", a trash row that never rendered, a list
that will not show a tenant just seeded. None of those look like what they
are, and each fix moves the failure one step earlier.

It runs two counts, and the second exists because the first was not enough.
Leftover tenants are the visible form the litter takes; the outage came from
something else. Memberships sharing the login subject accumulated one per run,
and login without a tenant hint authenticates against at most five candidate
rows — so past five the real membership was crowded out and a correct password
was refused. Twenty-two specs failed on login while the tenant count sat
nowhere near its threshold, which is to say the check that existed would have
called the environment healthy. The second count measures that mechanism
directly.

"Fixture-shaped" means the slug ends in the 8-hex suffix `uniqueSlug` mints
(`FIXTURE_SLUG_RE` in `fixtures/unique.ts`). Prefixes were the obvious
candidate and are unusable — the suite seeds `acme`, `ok`, `obj`, `big`,
`key`, `sse`, `e2e-user` and whatever a caller passes — while the suffix comes
from one function. Because the count depends on that, the gate's first test
asserts a freshly minted slug still matches, so changing the generator fails
loudly instead of quietly blinding the guard.

The gate never deletes anything. To clear a full stack:

```bash
docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml down -v
```

## The FR-008 test (capabilities.spec.ts T032)

The single most important test in the suite. Fires
`CapabilityService.Issue` twice via direct Connect-RPC with the
SAME pinned `Idempotency-Key`. Asserts both calls return the
same capability ID (server-side reflective replay) and the UI
shows exactly one row. Removing `Issue` from the idempotency
matcher OR flipping `RequireOnCreate` to false will fail this
test — keeping the middleware's recently-shipped behaviour
honest.

## Iterating on a single test

```bash
# Stack in a separate terminal (faster re-runs):
pnpm run test:e2e:stack

# Then in another terminal:
pnpm exec playwright test capabilities.spec.ts --headed
pnpm exec playwright test capabilities.spec.ts --debug
```

## Inspecting a failure

```bash
pnpm exec playwright show-report tests/e2e/test-results/html-report
# Or open a trace directly:
pnpm exec playwright show-trace tests/e2e/test-results/<failed-test-dir>/trace.zip
```

## Troubleshooting

| Symptom                                          | Likely cause                   | Fix                                                        |
| ------------------------------------------------ | ------------------------------ | ---------------------------------------------------------- |
| `webServer` times out                            | Paladin backend image not built | `task -d backend build:image`                              |
| `required variable PALADIN_E2E_S3_ACCESS_KEY`     | Garage creds not exported      | Re-run the two `export` commands in Quick start            |
| Backend logs `dial tcp 3900: connection refused` | port-forward not running       | `kubectl port-forward -n garage svc/garage-s3 3900:3900 &` |
| Tests pass once, fail on second run              | Stale postgres data            | `pnpm run test:e2e:stack:down` then `:stack`               |

For the full troubleshooting table + SDD context, see
[`specs/001-frontend-playwright-e2e/quickstart.md`](../../../specs/001-frontend-playwright-e2e/quickstart.md).
