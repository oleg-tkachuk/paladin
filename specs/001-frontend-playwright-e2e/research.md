# Phase 0 Research — Frontend Playwright E2E

All `NEEDS CLARIFICATION` markers from the spec were resolved
during `/speckit-clarify` (5/5 questions answered). This file
records the supporting research for each decision plus
findings on the technology choices the plan makes.

## R-001 — Playwright as the test framework

- **Decision**: `@playwright/test` `^1.60.0` (latest stable as
  of 2026-05-27).
- **Rationale**:
  - First-party browser automation from Microsoft; large
    community; mature for ~5 years.
  - Built-in trace viewer + video recording — exactly the
    artifact bundle FR-006 requires.
  - Native TypeScript support matches the application's
    language; no transpile step in the test fixtures.
  - Auto-waiting on actionability (`expect(locator).toBeVisible()`)
    drastically reduces the kind of arbitrary `setTimeout`
    flake that threatens SC-003.
- **Alternatives considered**:
  - **Cypress**: closer to "BDD with a UI" but its single-tab
    model can't easily exercise the multi-tab edge case
    from the spec, and the auth-bypass UX shortcuts it
    encourages would violate FR-003.
  - **WebdriverIO + Selenium**: more verbose, slower, less
    modern test-runner ergonomics. No upside for our scope.
  - **Puppeteer**: lower-level; we'd be reinventing test
    runner + reporter on top.
- **Version verification**:
  ```
  $ npm view @playwright/test version
  1.60.0
  ```

## R-002 — Garage as the S3-compatible backend

- **Decision**: Consume the **cluster-shared** Garage deployed
  by `gitops/specs/001-garage-object-storage`. The e2e
  docker-compose stack does NOT bring up its own Garage
  sidecar — it points the Paladin backend at the cluster service.
- **Rationale**:
  - Single source of truth: one Garage instance per cluster,
    shared by every consumer (Paladin control plane, e2e tests,
    future tooling). Avoids drift between
    test-fixture-version vs prod-version.
  - Same credentials path as production: the
    `garage-paladin-credentials` K8s Secret in the `paladin`
    namespace, populated by the gitops addon. Operator
    extracts via `kubectl get secret` and exports as
    `PALADIN_E2E_S3_ACCESS_KEY` / `_SECRET_KEY` env vars before
    `compose up`. Compose fails fast (`?:` required-var
    gate) if either is missing.
  - Local docker-compose can't directly resolve the
    in-cluster DNS name. Operator runs `kubectl port-forward
    -n garage svc/garage-s3 3900:3900` in a separate
    terminal; the backend containers then reach Garage via
    `host.docker.internal:3900` (overridable via
    `PALADIN_E2E_S3_ENDPOINT`).
- **What Garage gives us**:
  - Implements every S3 operation Paladin backend actually
    calls: `PutObject`, `GetObject`, `HeadObject`,
    `DeleteObject`, `ListObjects(V2)`, `CreateBucket`,
    `DeleteBucket`, Sigv4 auth, **presigned URLs**, multipart
    upload (the critical surface for Paladin's capability tokens
    and admin object operations).
  - Object versioning / tagging at the S3 layer are **not**
    implemented by Garage. Confirmed safe: Paladin tracks
    versions in the `object_versions` Postgres table and
    tags in `object_tags`. Neither hits S3 API calls.
- **Alternatives considered**:
  - **Spin up Garage in the test-stack docker-compose**:
    initial design. Rejected per operator directive — Garage
    is a platform service, not a test-fixture. Bundling it
    would mean two Garage deployments to keep in sync.
  - **MinIO**: equally capable, broader installed base.
    Wrong shape for "consume a cluster service" — MinIO
    isn't what gitops deploys.
  - **SeaweedFS**: matches the local minikube overlay today,
    but gitops is migrating to Garage; we follow.

## R-003 — Database isolation strategy

- **Decision**: Shared Postgres + UUID-suffixed per-test
  fixtures.
- **Rationale**:
  - Per-test database creation (TEMPLATE-based) costs
    ~500ms × test count → blows 3-minute budget on 6 tests
    + bootstrap.
  - Per-test schema would require sqlc rebuild per schema —
    impractical.
  - UUID-suffixed slugs/display-names + the existing
    `migrations/033_tenant_identity_hardening.sql`
    UNIQUE-on-slug enforcement means concurrent tests can't
    collide naturally.
- **Implementation**:
  - `frontend/tests/e2e/fixtures/unique.ts` exports
    `uniqueSlug(prefix)` returning `${prefix}-${
    crypto.randomUUID().slice(0, 8)}` (8 hex chars = ~32
    bits of entropy, more than enough for ≤4 parallel tests).

## R-004 — Seeded admin credential delivery

- **Decision**: Fixed dev-only credential hardcoded in
  `frontend/tests/e2e/fixtures/credentials.ts` and
  `docker-compose.test.yaml` env vars. Subject
  `e2e-admin@local`, password `e2e-not-a-secret-2026`.
- **Rationale**:
  - Matches the `local-dev-admin-pw-2026-change-me` pattern
    already established in `gitops/.../overlays/local/
    values/paladin/paladin.yaml`.
  - Deterministic — operator debugging a failed test can
    log in manually with the same credential to inspect
    DB state.
  - Per-run randomization would require an extra "read the
    password from disk" step in every helper — slower to
    write, slower to debug.
- **Compliance with Constitution V**:
  - `credentials.ts` MUST carry the comment
    `// NEVER true in prod. e2e-only fixture credentials.`
    above the export.
  - `docker-compose.test.yaml` MUST set
    `PALADIN_BOOTSTRAP_ADMIN_PASSWORD=e2e-not-a-secret-2026`
    inside a clearly-commented env block.

## R-005 — Browser engine matrix

- **Decision**: Chromium only.
- **Rationale**: see Clarification Q1. Paladin is an internal
  operator tool; cross-browser parity is not a stated
  requirement. Multi-engine matrix triples test runtime AND
  the flake surface (each engine has its own auto-wait
  quirks).
- **How to add Firefox/WebKit later** (if a cross-browser
  bug ever surfaces): a one-line change to `projects:` in
  `playwright.config.ts` extends coverage. The fixture API
  is engine-agnostic by design.

## R-006 — Test stack topology

- **Decision**: Per-plane container layout mirroring the
  existing `backend/deploy/docker-compose.yaml`, dropping
  the planes the test suite never touches.
- **Rationale**:
  - The "all-in-one" server mode was explicitly removed in
    `cmd/server/serve.go` (see comment at line 17: *"The
    collapsed all-in-one mode that used to live in rootCmd
    is intentionally absent"*). One container per plane is
    the supported topology.
  - The Playwright suite never exercises the `ingest`,
    `worker`, `mcp`, or `dispatcher` planes — those handle
    background jobs (event dispatch, outbox flush, MCP tool
    calls) that the UI tests don't drive. **Drop them from
    the test stack** to save container startups.
  - Net topology: `postgres`, `migrate` (one-shot),
    `bootstrap` (one-shot), `api`, `admin`, `ui`. Six
    services in compose; Garage is an EXTERNAL prerequisite
    (port-forwarded from the cluster — see R-002).
  - Three near-instant containers (postgres ~3s, migrate
    ~5s, bootstrap ~2s) + three longer-running (api ~8s,
    admin ~8s, ui ~5s).
  - Total cold-start ~25s (Garage is already up in the
    cluster, no boot cost). Suite runs 6 tests × ~15s avg
    ≈ 1.5 min with 4 workers. Combined: ~2 min wall-clock —
    comfortably under the 3-minute SC-002 budget.
- **Alternative considered and rejected**:
  - Multi-process container via `supervisord` running api +
    admin + iam in one image: avoids 2 container startups
    but ships a non-supported topology and breaks parity
    with how Paladin runs in prod. Not worth ~10s saved.

## R-007 — Playwright parallelism + flake guard

- **Decision**: Default parallelism (`workers = os.cpus()/2`,
  capped at 4). Retry policy: 1 retry on first failure
  ONLY when running with `--ci` flag; 0 retries locally so
  flake is surfaced not masked.
- **Rationale**:
  - 6 tests on 4 workers = 1.5 batches → ~30s real wall-clock
    if each test averages 15s → fits 3-minute budget with
    headroom.
  - 0 retries locally enforces SC-003's "zero flake across
    10 runs" — a passing-on-retry test is a flaky test
    that should be fixed before sign-off.
  - The `--ci` retry policy is forward-compatible with the
    future CI integration without changing the local
    contract.

## R-008 — Package manager: pnpm

- **Decision**: `pnpm@^11.3.0` (latest stable). Replaces npm
  for the `frontend/` workspace. `packageManager` field in
  `package.json` pins the version; `package-lock.json` is
  removed and `pnpm-lock.yaml` checked in.
- **Rationale**:
  - Strict dependency resolution: pnpm refuses to resolve
    imports for packages not declared in `package.json`,
    catching accidental phantom-dependency imports that
    npm's flat node_modules silently allows. Particularly
    valuable given Playwright pulls in deep dev trees.
  - Content-addressable store on disk: subsequent installs
    across worktrees / branches share the global store,
    ~3× faster cold install vs npm. Material on the CI
    runner when the test stack's frontend container
    builds.
  - Workspaces are first-class — leaves room for a future
    multi-package frontend refactor without retooling.
- **Alternatives considered**:
  - **Stay on npm**: zero migration cost, but every CI run
    re-resolves the dependency tree from scratch + retains
    the phantom-dep risk.
  - **yarn**: equivalent feature surface to pnpm but slower
    in benchmarks and the project's other tooling
    (Taskfile, lefthook) doesn't reference yarn anywhere.
  - **bun**: too aggressive a runtime swap for a Next.js 16
    workspace targeting node:22 in prod. Not on the
    LATEST_STABLE GA track yet for our use case.
- **Migration steps** (executed in T001 + new T001a):
  1. `corepack enable && corepack prepare pnpm@11.3.0 --activate`
  2. Delete `frontend/package-lock.json`.
  3. Run `pnpm install` from `frontend/` — generates
     `pnpm-lock.yaml`.
  4. Add `"packageManager": "pnpm@11.3.0"` to
     `frontend/package.json`.
  5. Verify `frontend/.npmrc` (if any) doesn't conflict
     with pnpm defaults; add `strict-peer-dependencies=false`
     if existing peer-dep complaints surface.
- **Scope warning**: This decision changes the dev tool for
  the entire `frontend/` workspace, not just the e2e
  subfolder. The migration ships in T001 because it's a
  Setup prerequisite for installing `@playwright/test` via
  pnpm. Per Constitution V (Local-Dev Parity), no overlay
  change needed — pnpm is a dev-time tool, not a runtime
  artifact.
