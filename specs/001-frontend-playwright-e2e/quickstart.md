# Quickstart — Frontend Playwright E2E

One-page operator manual for running and extending the suite.

## Prerequisites

- Docker Desktop (or equivalent — Colima, Rancher Desktop)
  with ≥ 4 CPU / 6 GB RAM available to containers.
- Node.js 22+ (matches `frontend/package.json` engines).
- `pnpm` `^11.3.0` (project's package manager — pinned via the
  `packageManager` field in `frontend/package.json`). Easiest
  install path: `corepack enable && corepack prepare
  pnpm@11.3.0 --activate` (no separate `npm i -g pnpm` needed).
- 16 GB RAM developer-class machine (per SC-002).
- Latest PALADIN backend image present locally as
  `registry.local/paladin/paladin:latest` —
  build via `task -d backend build:image` if absent.
- **Garage** reachable from the docker-compose network.
  Garage is a cluster-shared service from gitops
  (`specs/001-garage-object-storage`); the test stack
  consumes it, does NOT bring up its own. Two paths to
  reach it locally:
  1. **kubectl port-forward** (recommended for laptop dev):
     ```bash
     kubectl port-forward -n garage svc/garage-s3 3900:3900 &
     ```
     The backend containers reach the host's forwarded port
     via `host.docker.internal:3900`.
  2. Inside the cluster: set `PALADIN_E2E_S3_ENDPOINT` to the
     in-cluster service URL
     (`http://garage-s3.garage.svc.cluster.local:3900`).
- **Garage credentials** exported as env vars before
  `compose up` — extract from the K8s Secret gitops
  provisions:
  ```bash
  export PALADIN_E2E_S3_ACCESS_KEY=$(kubectl -n paladin \
    get secret garage-paladin-credentials \
    -o jsonpath='{.data.accessKeyId}' | base64 -d)
  export PALADIN_E2E_S3_SECRET_KEY=$(kubectl -n paladin \
    get secret garage-paladin-credentials \
    -o jsonpath='{.data.secretAccessKey}' | base64 -d)
  ```
  Compose fails fast with a clear error if either is unset.

## First run

```bash
# From the repo root.
cd frontend

# One-time install of Playwright + browser binaries.
pnpm install
pnpm exec playwright install chromium

# Run the full suite. webServer config brings up the test
# stack automatically; tear-down happens on Playwright exit.
pnpm run test:e2e
```

Expected: ~2 minutes wall-clock on an M1+ machine, 6 tests
passing, no failures. Run again to confirm SC-003 (zero
flake — pass 10× in a row before sign-off).

## Iterating on a single test

```bash
# Bring up the stack in a separate terminal so subsequent
# Playwright runs reuse it (cuts startup from ~30s to ~2s).
pnpm run test:e2e:stack

# In another terminal:
pnpm exec playwright test capabilities.spec.ts --headed

# Debug mode (Playwright Inspector — step through, edit
# locators interactively).
pnpm exec playwright test capabilities.spec.ts --debug
```

## Inspecting a failure

```bash
# HTML report is generated under tests/e2e/test-results/html-report.
pnpm exec playwright show-report tests/e2e/test-results/html-report

# Or open the per-failure trace directly:
pnpm exec playwright show-trace \
  tests/e2e/test-results/<failed-test-dir>/trace.zip
```

The trace bundles the full DOM snapshot per action, network
log, and console messages — usually enough to diagnose
without re-running.

## Tearing down

```bash
pnpm run test:e2e:stack:down
```

Removes containers + the tmpfs Postgres volume. Next run
starts from zero.

## Adding a new test

1. **Pick the right file**. Each user story has its own
   `*.spec.ts`. New flows belong with their conceptual
   sibling (e.g. a new bucket-policy test lives in
   `buckets.spec.ts`, not a new file).
2. **Seed fixtures** in `test.beforeEach` via the helpers
   from `fixtures/seed.ts` — never via the UI. UI seeding
   would inflate runtime and obscure what each test
   actually verifies.
3. **Drive the UI** through real form/page interactions —
   `page.fill()`, `page.click()`, `page.getByRole()`. No
   API shortcuts past the initial seed (FR-003).
4. **Assert via Playwright auto-wait locators**. Never
   `setTimeout` or `waitForTimeout`. Use
   `expect(locator).toBeVisible()` — it polls until the
   condition holds or the action timeout fires.
5. **Match the existing flake budget** — if your test
   needs a sleep, it has a bug. Open an issue describing
   the timing dependency instead of adding the sleep.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `webServer` times out after 120s | PALADIN backend image not built locally | `task -d backend build:image` |
| `compose up` errors `required variable PALADIN_E2E_S3_ACCESS_KEY is missing` | Garage credentials not exported from the kubectl Secret | Run the two `export PALADIN_E2E_S3_*` commands from the Prerequisites section |
| Backend containers log `s3: dial tcp 3900: connection refused` | `kubectl port-forward` not running or stopped | Restart `kubectl port-forward -n garage svc/garage-s3 3900:3900 &` |
| Backend can reach Garage but every bucket op 403s | Wrong key/secret pair (e.g. extracted from a different cluster's Secret) | Re-extract from the current cluster context: `kubectl config current-context` then re-export PALADIN_E2E_S3_* |
| Postgres healthcheck never passes | Conflicting postgres on host port 5432 | Bind to 5434 (already the default in compose.test.yaml) |
| Tests pass locally, fail with "Idempotency-Key missing" on a Create | Frontend transport not loaded | Verify `src/lib/connect/transport.ts` import chain compiles; restart `pnpm run dev` |
| Login form returns "user not found" | Bootstrap container didn't run | Check `docker compose -p paladin-e2e logs bootstrap`; bootstrap depends on migrate succeeding |
| Test passes on first run, fails on second | Stale data in the shared DB | Run `pnpm run test:e2e:stack:down` then `:stack` — should never need this in steady state; if it does, the test isn't UUID-suffixing all unique fields |

## Verifying the regression-coverage promise (SC-004)

Before sign-off, dry-run each of the six failure modes the
suite is supposed to catch:

| Surface to break | How | Suite should fail in |
|---|---|---|
| AuthGate redirect | Delete the `useEffect` in `frontend/src/components/AuthGate.tsx` | `auth.spec.ts` |
| Tenant scope persistence | Comment the `setScope` call in `ScopeContext` | `scope.spec.ts` |
| Bucket → ObjectKey navigation | Remove the row click handler in `BucketDetail` | `buckets.spec.ts` |
| Idempotency double-submit collapse | Set `RequireOnCreate: false` in `build_listeners_admin.go` | `capabilities.spec.ts` |
| Tenant restore | Comment out `RestoreTenant` in the trash page button handler | `trash.spec.ts` |

Each should produce a clear, named test failure — not a
mystery timeout. If any of these breaks silently in the
suite, that test is mis-asserting and needs to be fixed
before sign-off.
