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

- **Decision**: `dxflrs/garage:v2.3.x` (single binary,
  `--single-node` mode).
- **Rationale**:
  - Single-container deployment, env-var bootstrap
    (`GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY`,
    `GARAGE_DEFAULT_BUCKET`) — matches the deterministic
    test-stack-init shape we want.
  - Rust runtime → low memory footprint (~50 MB resident),
    fast cold start (<2s).
  - Implements every S3 operation PALADIN backend actually calls:
    `PutObject`, `GetObject`, `HeadObject`, `DeleteObject`,
    `ListObjects(V2)`, `CreateBucket`, `DeleteBucket`,
    Sigv4 auth, **presigned URLs**, multipart upload (the
    critical surface for PALADIN's capability tokens and admin
    object operations).
- **Alternatives considered**:
  - **MinIO**: equally capable, broader installed base. Same
    bootstrap shape. Rejected because Garage aligns with the
    "lightweight self-hosted OSS-first" stack philosophy of
    the broader gitops, and is materially lighter on
    resources for an identical-feature surface.
  - **SeaweedFS**: matches the local minikube overlay but
    needs ~4 containers (master + volume + filer + s3) — eats
    the 3-minute budget.
  - **LocalStack S3**: AWS-flavoured emulation, JVM-based, ~6s
    cold start. Overkill — we don't need AWS-specific
    behaviours.
- **Risks**:
  - Garage is missing S3-level object versioning and
    object/bucket tagging. **Verified safe**: PALADIN tracks
    versions in the `object_versions` Postgres table and
    tags in `object_tags`. Neither is implemented via S3
    API calls from the PALADIN backend. (Confirmed by
    `rtk grep "PutObjectTagging\|GetObjectVersion" backend/`
    returning zero hits in the storage adapter.)

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
- **Rationale**: see Clarification Q1. PALADIN is an internal
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
  - Net topology: `postgres`, `paladin-migrate` (one-shot),
    `paladin-bootstrap` (one-shot), `paladin-api`, `paladin-admin`,
    `garage`, `paladin-ui`. Seven services, but four of them are
    near-instant (postgres ready in ~3s, migrate ~5s,
    bootstrap ~2s, garage <2s). Backend planes are the
    bottleneck at ~8s each.
  - Total cold-start ~30s. Suite runs 6 tests × ~15s avg ≈
    1.5 min with 4 workers. Combined: ~2 min wall-clock —
    comfortably under the 3-minute SC-002 budget.
- **Alternative considered and rejected**:
  - Multi-process container via `supervisord` running api +
    admin + iam in one image: avoids 2 container startups
    but ships a non-supported topology and breaks parity
    with how PALADIN runs in prod. Not worth ~10s saved.

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
