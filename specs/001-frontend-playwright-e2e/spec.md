# Feature Specification: Frontend Playwright E2E Test Suite

**Feature Branch**: `001-frontend-playwright-e2e`

**Created**: 2026-05-27

**Status**: Draft

**Input**: User description: "Frontend Playwright E2E test suite for the Paladin admin UI — automated regression coverage for the critical operator journeys (login, AuthGate, tenant scope switching, bucket browsing, capability lifecycle, tenant restore from trash). Replaces today's manual smoke testing on minikube. Must be reliable, fast, and authored in the same TypeScript flavour as the application."

## Clarifications

### Session 2026-05-27

- Q: Which browser engines should the suite run against? → A: Chromium only. Cross-engine coverage adds little for an internal admin tool serving developer-class operators and triples CI cost. Firefox/WebKit can be added later if a real cross-browser bug surfaces.
- Q: Should the suite integrate with a specific CI platform for artifact upload? → A: No — CI integration is explicitly deferred. v1 is locally runnable only; Playwright's failure artifacts (screenshot, video, trace) land in a known local directory (`frontend/tests/e2e/test-results/`). When CI lands later, the artifact path will already exist and only the upload step will need wiring.
- Q: How are tests isolated from each other at the database layer? → A: Shared Postgres for the entire run; each test seeds its own fixtures (`seedTenant()`, etc.) with UUID-suffixed identifiers (e.g. `slug: "acme-" + crypto.randomUUID().slice(0,8)`) to avoid uniqueness collisions without per-test schema churn. Cheap and fast — keeps the 3-min budget intact.
- Q: How is the seeded admin credential delivered to the test stack? → A: Fixed dev-only credential hardcoded in the test stack config (e.g. `e2e-admin / e2e-not-a-secret-2026`). Mirrors the `local-dev-admin-pw-*` pattern already used in `gitops` overlays. MUST carry an inline `NEVER true in prod` comment per Constitution Principle V. Tradeoff: weaker than per-run-generated, but keeps bootstrap deterministic and lets a debugging operator log in manually when a test fails.
- Q: Which S3-compatible backend powers the test stack? → A: **Garage** (`dxflrs/garage:v2.3.0` or later). Single binary, env-var bootstrap (`GARAGE_DEFAULT_ACCESS_KEY/SECRET_KEY/BUCKET`), Rust runtime, fast startup. Supports every S3 operation Paladin backend actually calls: PutObject, GetObject, ListObjects, HeadObject, CreateBucket, Sigv4 + presigned URLs, multipart upload. Garage's "missing" features (object versioning, tagging at the S3 layer) don't matter because Paladin tracks those in Postgres (`object_versions`, `object_tags` tables), not via S3 API calls. Aligned with the lightweight-OSS philosophy of the broader gitops stack.
- Q: Which package manager? → A: **pnpm** (`^11.3.0`). Adopting pnpm requires migrating the frontend from `package-lock.json` → `pnpm-lock.yaml` and pinning the package manager via the `"packageManager": "pnpm@11.3.0"` field in `package.json`. Rationale: faster install (~3× cold), strict dependency resolution (avoids accidental phantom-dep imports — relevant because Playwright pulls in deep dev trees), better disk usage via content-addressable store. The migration touches `frontend/` only; backend tooling stays unchanged. Implies a new Setup task to perform the lockfile migration in the same commit train as the Playwright dep adoption.
- Q: Garage in the test stack — sidecar container or external dependency? → A: **External cluster service.** Garage is deployed by gitops (`specs/001-garage-object-storage`) as a platform service shared across consumers — the test stack consumes it via `kubectl port-forward` (laptop dev) or in-cluster DNS (CI-on-cluster). The Garage container previously planned for docker-compose.test.yaml is removed. Credentials come from the K8s Secret `garage-paladin-credentials` that gitops provisions; the compose file requires `PALADIN_E2E_S3_ACCESS_KEY` + `PALADIN_E2E_S3_SECRET_KEY` env vars (gated via `?:` fail-fast) so missing creds produce a clear error instead of a runtime S3 failure.
- Q: Does the idempotency gate cover `CapabilityService.Issue`? → A: **Now yes — extended during /speckit-implement Phase 6.** US4's premise (double-submit of CreateCapability collapses via the middleware) initially didn't hold: the RPC is named `Issue`, not `Create*`, so neither the backend matcher (`isMutationMethod`) nor the frontend transport interceptor recognised it. Option C ships a two-place extension: `isMutationMethod` and the transport matcher now accept BOTH `Create*` and `Issue*` prefixes (with explicit comments noting the policy expansion). The change is symmetric — server enforces, client auto-injects, the contract holds end-to-end. Future mutation-shaped verbs (e.g. `Submit*`) would need an analogous two-place update; documented inline.

## User Scenarios & Testing *(mandatory)*

The user stories below are ordered by operator-journey criticality.
Every story is independently testable — implementing just **US1**
already yields working regression coverage for the most failure-
prone surface (auth boundary). Subsequent stories layer additional
journeys without depending on the previous ones at runtime (they
share fixtures, not state).

### User Story 1 — Login + AuthGate redirect coverage (Priority: P1)

An operator opens a deep link to an authed page (e.g.
`/tenants`) in a browser with no active session. The application
must redirect them to `/login?next=/tenants`, accept their
credentials, and bounce them back to the originally requested URL
with the page fully rendered. This is the security contract of
the AuthGate — without it, a stale tenant page could flash data
before redirect, or worse, the redirect could be missed entirely
and unauthenticated traffic could see the page chrome.

**Why this priority**: This is the highest-risk surface in the
application. A regression here means either a security leak
(unauthenticated user sees data) or a denial-of-service for
legitimate users (the bounce-back loop breaks). Manual smoke
testing catches it only when the operator happens to test that
exact flow.

**Independent Test**: Drive a fresh browser session to a deep
authed URL, assert the redirect URL, complete the login form,
assert landing on the deep URL with content visible. No
dependency on any other story.

**Acceptance Scenarios**:

1. **Given** a fresh browser session with no cookies and no
   localStorage, **When** the operator navigates to `/tenants`,
   **Then** the URL becomes `/login?next=%2Ftenants` and the
   login form is visible.
2. **Given** the operator is on `/login?next=/tenants` with a
   valid seeded admin credential pair, **When** they submit the
   form, **Then** the URL becomes `/tenants` and the tenant
   list renders within 2 seconds.
3. **Given** the operator is on `/login` with **no** `next`
   query (default entry), **When** they submit, **Then** they
   land on a sensible default landing page (e.g. `/` or
   `/tenants`) — not on `/login` (loop prevention).
4. **Given** the operator submits invalid credentials, **When**
   the response returns, **Then** an error message is visible
   on the form, the URL stays on `/login`, and no session
   cookie is set.

---

### User Story 2 — Backend + bucket scope switching (Priority: P1)

> **Spec history**: This story originally read "Tenant scope
> switching." During /speckit-implement Phase 4 it became clear
> the current UI does NOT support tenant switching — the
> tenant field on `<ScopePicker>` is read-only because of the
> 1:1 user-tenant model (see `frontend/src/context/
> ScopeContext.tsx:23–29`). Cross-tenant switching is a
> planned backend feature (`ListMyMemberships +
> SwitchTenant`); a BACKLOG entry tracks it in Paladin's
> `BACKLOG.md`. This story is therefore re-scoped to the
> scope-axis the UI ACTUALLY exposes today: backend + bucket
> selection.

An operator picks a different backend or bucket from the
`<ScopePicker>` popover in the topbar. The topbar breadcrumb
reflects the new selection immediately. The choice persists
in localStorage and survives a browser reload — operators
should not have to re-select their working scope every time
they refresh.

**Why this priority**: The scope picker is the central UX
abstraction for navigating storage hierarchies. A regression
here (picker doesn't update, localStorage isn't read on
mount, navigation drops the scope) makes the UI feel broken
even when every backend RPC is healthy.

**Independent Test**: Log in, open the scope picker, select a
seeded bucket, verify the topbar reflects it; reload the
page, verify the scope is still selected.

**Acceptance Scenarios**:

1. **Given** the operator is logged in with at least one
   bucket visible in the picker, **When** they click the
   scope-picker trigger in the topbar, **Then** the popover
   opens and the Backends + Buckets sections render with the
   current selection highlighted.
2. **Given** the picker is open and the current bucket is
   "Any bucket", **When** they click a specific seeded
   bucket, **Then** the popover closes AND the topbar
   breadcrumb's bucket segment updates from the muted-italic
   "any" to the selected bucket's name.
3. **Given** the operator has selected a specific bucket,
   **When** they reload the page, **Then** the topbar
   breadcrumb still shows the selected bucket (the
   `setScope` write to localStorage round-trips through the
   `safeRead` hydration in `ScopeContext`).

---

### User Story 3 — Bucket list & object key open (Priority: P2)

An operator navigates to the bucket list, picks a bucket, opens
its ObjectKeys panel, and opens one ObjectKey's detail. This is
the most common day-1 operator path: "I need to see what's in
this tenant's storage."

**Why this priority**: High operational value (everyday use),
moderate risk (the data is read-only so a regression here
doesn't corrupt state). Worth automating because the surface
has multiple navigation depths and any of them can break in
isolation.

**Independent Test**: Seed a tenant with one bucket and one
ObjectKey, navigate the operator through `/buckets → bucket
detail → objectkeys → objectkey detail`, assert the final
page shows the seeded ObjectKey's name.

**Acceptance Scenarios**:

1. **Given** a tenant with at least one bucket and one
   ObjectKey, **When** the operator lands on `/buckets`,
   **Then** the bucket list shows at least one row matching
   the seeded bucket's display name.
2. **Given** the operator clicks the bucket row, **When** the
   bucket detail page loads, **Then** the ObjectKeys panel is
   visible and shows the seeded key's name.
3. **Given** the operator opens the ObjectKey detail, **When**
   the detail view renders, **Then** the canonical resource
   name and the policy editor are both visible.

---

### User Story 4 — Capability create + revoke (Priority: P2)

An operator creates a capability token through the
`/capabilities` form, verifies it appears in the list, and
revokes it. The form submits a Create RPC; the transport layer
auto-injects an `Idempotency-Key` header. A double-submit
(double-click, network retry) must return the SAME row — not
two. Revocation flips the row's state to "revoked" without
removing it from the table.

**Why this priority**: This is the only operator-facing path
that exercises the idempotency contract end-to-end. A
regression here means double-submits silently create duplicate
capabilities — a security problem (leaked secrets, broken
revocation lists). Also exercises the Create*/Delete* RPC pair.

**Independent Test**: Open the capability create form, submit
once, assert one row appears. Re-submit the same form
(simulated double-click) and assert STILL one row, not two.
Revoke the row and assert the state column updates.

**Acceptance Scenarios**:

1. **Given** the operator is on `/capabilities` with no
   existing capabilities, **When** they fill the form and
   submit once, **Then** exactly one row appears in the list
   within 2 seconds.
2. **Given** the operator double-clicks the submit button
   rapidly, **When** both requests reach the backend, **Then**
   the list still shows exactly one capability (idempotency
   collapsed the duplicate).
3. **Given** a capability exists in active state, **When** the
   operator clicks revoke and confirms, **Then** the row
   updates to show "revoked" status WITHOUT being removed from
   the table.

---

### User Story 5 — Tenant restore from trash (Priority: P3)

An operator soft-deletes a tenant via the tenant detail page,
navigates to `/trash`, restores it, and verifies it reappears
in `/tenants`. Covers the soft-delete + restore RPC pair —
the foundation of the "oops, I deleted that" recovery flow.

**Why this priority**: Lower-frequency operator action, but
critical when it fires (a wrongly-deleted tenant means
inaccessible storage for whoever owns it). Worth automating to
catch regressions in the restore path, which has historically
been less exercised than delete.

**Independent Test**: Seed a tenant, soft-delete it, restore
from trash, assert it reappears in the active tenant list.

**Acceptance Scenarios**:

1. **Given** a tenant exists in active state, **When** the
   operator soft-deletes it (with valid resource_version),
   **Then** it disappears from `/tenants` and appears in
   `/trash`.
2. **Given** the tenant sits in `/trash`, **When** the
   operator clicks restore, **Then** the tenant reappears in
   `/tenants` within 2 seconds and is removed from `/trash`.

*Note*: a "slug collision on restore" scenario was initially
planned but is unreachable in the current backend — the slug
UNIQUE constraint applies across active **and** trashed sets
(see comment in `backend/migrations/036_soft_delete_tenants.sql`),
so a colliding slug is caught at CREATE or RENAME time, never
at RESTORE. The scenario will be added when the BACKLOG'd
"partial UNIQUE on active rows only" change lands.

---

### Edge Cases

- Session expires mid-test (refresh token revoked between two
  page navigations) — UI must surface a re-login prompt instead
  of silently looping on 401s.
- Backend returns 503 during a Create RPC submission — UI must
  show a retry affordance and NOT silently swallow the error.
- Slow network simulation (3G throttle) — the AuthGate
  loading splash must appear instead of a flash of unauthed
  content.
- Idempotency-Key collision (extremely unlikely UUIDv4) — the
  cached prior response must still be served correctly without
  the UI showing a stale state.
- Two browser tabs open on the same session: scope change in
  tab A must NOT silently rewrite tab B's URL or data view.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The test suite MUST cover at minimum the five
  user stories above, with at least 6 distinct test cases
  (US1 deserves multiple — happy path, no-next default,
  invalid-credentials negative).
- **FR-002**: Each test MUST be independent — failure or
  success of one test MUST NOT affect any other. Tests share
  the same Postgres instance for the run, but seeded data is
  unique-per-test via UUID-suffixed identifiers
  (`seedTenant()` helpers append a random suffix to slugs,
  display names, and any other unique field). No test reads
  data created by another.
- **FR-003**: Tests MUST drive the browser through the real
  UI surfaces (form submission, button clicks, navigation)
  rather than bypassing the UI via direct API calls. The one
  exception: per-test seed data MAY be created via Connect-RPC
  calls in `test.beforeEach` to keep setup fast.
- **FR-004**: The seeded admin user MUST be provisioned by
  the test stack's bootstrap step (not hand-rolled per test)
  with a **fixed dev-only credential** hardcoded in the test
  stack config. The credential MUST carry an inline `NEVER
  true in prod` comment per Constitution Principle V. Tests
  log in via the real `/login` form using these credentials,
  exercising the full JWT + AuthGate chain.
- **FR-005**: The test stack MUST be runnable locally via
  a single command. Local CI parity is critical — anything
  that runs in CI runs identically on the developer's laptop.
- **FR-006**: On test failure, Playwright MUST produce the
  artifact bundle (screenshot, video, trace) in a known local
  directory (`frontend/tests/e2e/test-results/`). CI integration
  for uploading these artifacts is **explicitly out of scope
  for v1** — the suite is locally runnable only. Once CI lands
  in a follow-up, the upload step plugs into the existing
  artifact path without touching test code.
- **FR-007**: The suite MUST be authored in TypeScript using
  the same lint/format conventions as the application code
  (matching `frontend/src/`'s tsconfig + eslint).
- **FR-008**: At least one test MUST explicitly assert the
  idempotency contract: double-submitting a mutation-shaped
  request (Create* / Issue*) with the same `Idempotency-Key`
  yields exactly one resource row, not two. US4 implements
  this against `CapabilityService.Issue` (the motivating
  case for extending the gate to cover `Issue*` — see
  Clarifications Q8). This is the only observable surface
  where the recently-shipped middleware memoize-and-replay
  can regress silently.

### Key Entities

- **Test stack**: The composable runtime that supports the
  E2E suite — backend service, Postgres, S3-compatible object
  store, Next.js frontend in production-mode bundle (`next
  start`, not `next dev` — HMR is a documented flake source
  for E2E suites). Independent of production
  infrastructure; spun up per-CI-run or per-local-`run` and
  torn down afterward.
- **Seeded admin**: A known-credentials user provisioned at
  stack bootstrap time, holding platform-admin role. Reused
  across every test for the login step.
- **Per-test tenant fixture**: A unique tenant created in
  `beforeEach` via a `seedTenant()` helper. Lives only for the
  duration of one test; auto-cleanup is not required because
  every CI run starts from a fresh database.
- **Artifact bundle**: The per-failure trio of screenshot +
  video + trace.zip uploaded to CI for postmortem debugging.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: The suite covers at least the 6 user-story
  test cases (US1 may contribute multiple; US2–US5 contribute
  at least one each).
- **SC-002**: A full local run of the suite — Chromium engine
  only, including stack bootstrap — completes in under **3
  minutes** on a developer-class machine (M1+/Ryzen 5+ class,
  16 GB RAM). CI runner targeting is deferred along with the
  rest of CI integration (see FR-006).
- **SC-003**: Across **10 consecutive local runs**, zero tests
  show flaky behaviour (no test passes-then-fails-then-passes
  pattern without an intervening code change). Measured by
  running the suite 10× in a row immediately after first
  sign-off.
- **SC-004**: A regression in the AuthGate redirect contract,
  the scope switching, the bucket navigation, the capability
  create+revoke, the idempotency double-submit collapse, or
  the tenant restore flow MUST be caught by the suite —
  verified by intentionally breaking each surface in a
  dry-run before sign-off.
- **SC-005**: The BACKLOG.md entry for "Frontend Playwright
  suite" is removed in the closing commit of this feature
  (per Constitution Principle III: closed BACKLOG entries are
  deleted, not marked done).

## Assumptions

- The Paladin backend supports a "single-binary all-in-one" mode
  (or all six planes can be co-located on one container) for
  the test stack. If this isn't already true, the test stack
  may need to run multiple containers — adding ~10s to
  startup but not changing the test surface. Confirmed by
  inspection of `cmd/server` subcommands during planning.
- The test stack uses **Garage** (`dxflrs/garage`) as the
  S3-compatible backend (see Clarifications Q5). It supports
  every S3 op Paladin backend actually calls; tag/version S3
  operations Paladin would otherwise miss are not actually called
  by Paladin (they're tracked in Postgres tables instead).
- The test stack uses a clean, ephemeral Postgres per run —
  no shared schema state across CI invocations. Migration
  cost (running the full `goose up`) is acceptable inside the
  3-minute budget.
- Playwright's built-in retry logic (1 retry on CI) is
  acceptable masking for genuinely transient infrastructure
  hiccups (e.g. backend cold-start race). True flakiness in
  test code itself is forbidden — see SC-003.
- The seeded admin's password is a fixed dev-only credential
  hardcoded in the test stack config with a `NEVER true in
  prod` comment (see Clarifications Q4). This is not a
  production credential and MUST NEVER be reused on a real
  cluster.
- Mobile-first responsive testing is **out of scope** for v1.
  If desktop coverage proves insufficient, a separate spec
  will address mobile.
- Visual regression / screenshot diff is **out of scope**.
  Tests assert structural state (text present, URL shape,
  row count) not pixel-level layout.
