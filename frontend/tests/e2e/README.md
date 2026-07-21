# PALADIN Frontend E2E (Playwright)

Operator-facing UI regression guards for the PALADIN admin UI. 15
test scenarios across 5 user stories. Design rationale + the
SDD audit trail live at
[`specs/001-frontend-playwright-e2e/`](../../../specs/001-frontend-playwright-e2e/).

## Quick start

```bash
# Prerequisites (one-time):
#   - Docker daemon running
#   - PALADIN backend + UI images present locally as
#     registry.local/paladin/paladin-core:latest
#     registry.local/paladin/paladin-ui:latest
#     (build via `task -d backend build:image` if missing)
#   - pnpm 11.3.0+: `corepack enable && corepack prepare pnpm@11.3.0 --activate`
#   - Chromium binary: `pnpm exec playwright install chromium`
#   - Garage reachable on host port 3900: see "Garage" below.

cd frontend

# Export Garage credentials (required by docker-compose.test.yaml).
export PALADIN_E2E_S3_ACCESS_KEY=$(kubectl -n paladin \
  get secret garage-paladin-credentials \
  -o jsonpath='{.data.accessKeyId}' | base64 -d)
export PALADIN_E2E_S3_SECRET_KEY=$(kubectl -n paladin \
  get secret garage-paladin-credentials \
  -o jsonpath='{.data.secretAccessKey}' | base64 -d)

# Run the suite. Playwright's webServer config will bring up
# the docker-compose test stack and tear it down on exit.
pnpm run test:e2e
```

## Garage (external dependency)

Garage is a cluster-shared platform service from
[`gitops/specs/001-garage-object-storage`](../../../../gitops/specs/001-garage-object-storage/),
not bundled in the test stack. The PALADIN backend containers in
`docker-compose.test.yaml` point at `host.docker.internal:3900`,
so the operator MUST forward Garage to the host before
`compose up`:

```bash
kubectl port-forward -n garage svc/garage-s3 3900:3900 &
```

If `compose up` errors with `required variable PALADIN_E2E_S3_ACCESS_KEY
is missing`, the credential export above wasn't run.

## Layout

```
tests/e2e/
├── fixtures/
│   ├── credentials.ts     # seeded admin user (NEVER-in-prod marker)
│   ├── unique.ts          # uniqueSlug() — UUID-suffixed identifiers
│   ├── auth.ts            # loginAsAdmin(), logout() — UI-driven
│   └── seed.ts            # seedTenant, seedBucket, seedObjectKey,
│                          # seedCapability — Connect-RPC direct
├── auth.spec.ts           # US1 — login + AuthGate redirect (4 tests)
├── scope.spec.ts          # US2 — scope picker behavior (3 tests)
├── buckets.spec.ts        # US3 — bucket → ObjectKey navigation (3 tests)
├── capabilities.spec.ts   # US4 — capability lifecycle + FR-008 (3 tests)
├── trash.spec.ts          # US5 — tenant restore from trash (2 tests)
└── docker-compose.test.yaml   # 6 services: postgres, migrate,
                                 # bootstrap, api, admin, ui
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

| Symptom                                          | Likely cause                | Fix                                                        |
| ------------------------------------------------ | --------------------------- | ---------------------------------------------------------- |
| `webServer` times out                            | PALADIN backend image not built | `task -d backend build:image`                              |
| `required variable PALADIN_E2E_S3_ACCESS_KEY`        | Garage creds not exported   | Re-run the two `export` commands in Quick start            |
| Backend logs `dial tcp 3900: connection refused` | port-forward not running    | `kubectl port-forward -n garage svc/garage-s3 3900:3900 &` |
| Tests pass once, fail on second run              | Stale postgres data         | `pnpm run test:e2e:stack:down` then `:stack`               |

For the full troubleshooting table + SDD context, see
[`specs/001-frontend-playwright-e2e/quickstart.md`](../../../specs/001-frontend-playwright-e2e/quickstart.md).
