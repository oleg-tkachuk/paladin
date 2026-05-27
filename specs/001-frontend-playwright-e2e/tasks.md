---
description: "Tasks for the Frontend Playwright E2E feature"
---

# Tasks: Frontend Playwright E2E Test Suite

**Input**: Design documents from `/specs/001-frontend-playwright-e2e/`
**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/fixtures-api.md](contracts/fixtures-api.md), [quickstart.md](quickstart.md)

**Tests**: This feature IS the test suite — every "task" produces or verifies test code. There is no separate "production code + tests" split.

**Organization**: Tasks are grouped by user story. Each user story's phase is independently testable: completing US1 alone yields a working regression guard for the auth boundary; US2 adds scope switching; and so on.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks).
- **[Story]**: Maps to a user story from spec.md (US1…US5). Setup, Foundational, and Polish phases omit the story label.
- Every task carries an explicit file path.

## Path Conventions

This is a **web-application** layout (per [plan.md](plan.md) §"Structure Decision"). All new files live under `frontend/tests/e2e/` except the Playwright config (`frontend/playwright.config.ts`) and a small `package.json` edit. Backend and gitops are untouched.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Install Playwright + Chromium and create the directory skeleton. No tests run yet — these tasks just make the stack ready.

- [X] T001 Setup `frontend/` package manager + Playwright. Per Constitution II (single-scope commits), ship as **two sequential commits in this order**:
    1. **`build(frontend): migrate package manager from npm to pnpm`** (R-008, Clarification Q6) — (a) `corepack enable && corepack prepare pnpm@11.3.0 --activate`; (b) delete `frontend/package-lock.json`; (c) add `"packageManager": "pnpm@11.3.0"` to `frontend/package.json`; (d) from `frontend/`, run `pnpm install` — generates `pnpm-lock.yaml`; (e) sanity-check `pnpm run dev / lint / build` paths still resolve (pnpm scripts mirror npm's). **Workspace-wide tool swap** — call it out explicitly in the commit message because the change reaches beyond the e2e folder.
    2. **`build(frontend): add @playwright/test devDep`** — add `@playwright/test@^1.60.0` to `frontend/package.json` `devDependencies`; from `frontend/`, run `pnpm install` to write the lock entry.
- [X] T002 Add scripts (`test:e2e`, `test:e2e:headed`, `test:e2e:debug`, `test:e2e:stack`, `test:e2e:stack:down`) to `frontend/package.json` per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"package.json scripts (additions)"
- [X] T003 Run `pnpm exec playwright install chromium --with-deps` from `frontend/` to fetch the browser binary
- [X] T004 [P] Create the directory skeleton: `frontend/tests/e2e/`, `frontend/tests/e2e/fixtures/`, `frontend/tests/e2e/test-results/`
- [X] T005 [P] Append `frontend/tests/e2e/test-results/` and `frontend/test-results/` to `frontend/.gitignore`
- [X] T006 [P] Create `frontend/playwright.config.ts` per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"playwright.config.ts (root config)" — Chromium-only `projects`, `webServer` wired to docker-compose.test.yaml, retries 0 locally / 1 on `CI`, baseURL `http://localhost:3000`, screenshot/video/trace `retain-on-failure`

**Checkpoint**: After T006 `pnpm exec playwright test --list` should print "no tests found" cleanly (no syntax error on config).

---

## Phase 2: Foundational (Blocking Prerequisites for Every User Story)

**Purpose**: The test-stack and shared fixture helpers every story depends on. Nothing under Phase 3+ can run until Phase 2 completes — but stories CAN be implemented in parallel once these land.

- [X] T007 Create `frontend/tests/e2e/docker-compose.test.yaml` with **six** services (postgres, migrate, bootstrap, api, admin, ui) per [data-model.md](data-model.md) §"Entity: Test Stack". Compose project name `paladin-e2e`; postgres on tmpfs; healthchecks on every service; `condition: service_completed_successfully` for migrate/bootstrap dependencies. Garage is an **external** cluster-shared dependency (R-002, Clarification Q7) — operator port-forwards it; compose file requires `PALADIN_E2E_S3_ACCESS_KEY/_SECRET_KEY` env vars via `?:` fail-fast gate.
- [X] T008 [P] Create `frontend/tests/e2e/fixtures/credentials.ts` exporting `SEEDED_ADMIN` per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"`fixtures/credentials.ts`". File MUST carry the comment `// NEVER true in prod. e2e-only fixture credentials.` per Constitution Principle V.
- [X] T009 [P] Create `frontend/tests/e2e/fixtures/unique.ts` exporting `uniqueSlug(prefix)` and `uniqueDisplayName(prefix)` (8-hex-char UUID suffix) per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"`fixtures/unique.ts`"
- [X] T010 [P] Create `frontend/tests/e2e/fixtures/auth.ts` exporting `loginAsAdmin(page)` and `logout(page)` per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"`fixtures/auth.ts`". `loginAsAdmin` MUST drive the real `/login` form (FR-003) and assert the post-login URL is NOT `/login`.
- [X] T011 Create `frontend/tests/e2e/fixtures/seed.ts` with the `seedTenant(opts)` helper per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"`fixtures/seed.ts`". Use the generated Connect clients from `frontend/src/gen/`; cache the platform-admin Bearer token in module scope after the first IAM Login. `seedBucket` and `seedObjectKey` ship later in US3's phase (they're not needed for US1/US2).
- [~] T012 Verify the test-stack boots end-to-end: from `frontend/`, run `pnpm run test:e2e:stack` and confirm every service reaches `healthy` within 60s. Tear down with `pnpm run test:e2e:stack:down`. **PARTIAL** during the SDD impl run: `postgres` + `garage` services were verified healthy via `docker compose ... up postgres garage -d --wait`; the PALADIN backend / UI containers (api, admin, migrate, bootstrap, ui) require the locally-built `registry.local/paladin/paladin:latest` and `:paladin-ui:latest` images, which weren't reachable from this host's Docker daemon at impl time (they live in the in-cluster registry). **Operator follow-up**: from the repo root, run `task -d backend build:image` (or equivalent), then `cd frontend && pnpm run test:e2e:stack` and confirm all seven services reach `healthy` within 60 s before proceeding to Phase 3.

**Checkpoint**: After T012 the stack is provably runnable. User-story phases can now proceed in parallel (each `*.spec.ts` lives in its own file, no inter-test dependencies).

---

## Phase 3: User Story 1 — Login + AuthGate redirect (P1) 🎯 MVP

**Story goal**: Catch any regression to the AuthGate's deep-link → redirect → bounce-back contract — the highest-risk surface in the application.

**Independent test**: Run `pnpm exec playwright test auth.spec.ts` against a fresh stack. The four scenarios in [spec.md](spec.md) §US1 must all pass; intentionally breaking the `useEffect` in `frontend/src/components/AuthGate.tsx` must fail at least one scenario (per [quickstart.md](quickstart.md) SC-004 dry-run).

- [X] T013 [US1] Create `frontend/tests/e2e/auth.spec.ts` with the file-level imports (`@playwright/test`, `./fixtures/credentials`) and a `describe("US1 — Login + AuthGate")` block. No `beforeEach` here — each scenario starts from a fresh context.
- [X] T014 [US1] Implement scenario "deep-link redirect" in `auth.spec.ts`: navigate to `/tenants` with a fresh `browser.newContext()`, assert URL becomes `/login?next=%2Ftenants` and the email input is visible.
- [X] T015 [US1] Implement scenario "post-login bounces back to next" in `auth.spec.ts`: from `/login?next=/tenants`, submit the form with `SEEDED_ADMIN` credentials, assert URL becomes `/tenants` and at least one tenant row (or empty-state message) is visible within 2 s.
- [X] T016 [US1] Implement scenario "default landing on no `next`" in `auth.spec.ts`: navigate to `/login` directly, submit, assert landing on a non-`/login` URL (loop prevention).
- [X] T017 [US1] Implement scenario "invalid credentials" in `auth.spec.ts`: submit `/login` with a wrong password, assert an error message appears in the form, URL stays on `/login`, and `page.context().cookies()` contains no session cookie.
- [~] T018 [US1] Run `pnpm exec playwright test auth.spec.ts` ten times consecutively (`for i in {1..10}; do pnpm exec playwright test auth.spec.ts || break; done`); zero flake (SC-003 partial verification for US1). **DEFERRED to operator runtime** alongside T012 — requires the full stack up (PALADIN images built locally + Garage port-forwarded). Static gates verified during SDD impl: TypeScript `pnpm exec tsc --noEmit` exits clean; `pnpm exec playwright test --list auth.spec.ts` discovers all four scenarios with valid syntax.

**Checkpoint**: US1 done. MVP slice is shippable as-is — the most failure-prone UI contract has a regression guard.

---

## Phase 4: User Story 2 — Backend + bucket scope switching (P1)

> **Re-scoped during /speckit-implement.** Original "Tenant scope switching" wording assumed a cross-tenant picker that doesn't exist in the current UI (`ScopeContext.tsx:23–29`: *"the 1:1 user-tenant model means switching tenants requires a different login"*). Spec.md §US2 rewritten + BACKLOG entry "Cross-tenant scope switcher" added. Tests now exercise the scope axes the UI ACTUALLY exposes today (backend + bucket).

**Story goal**: Catch any regression where opening the scope picker, selecting a different bucket, or reloading the page silently drops the operator's working scope.

**Independent test**: Run `pnpm exec playwright test scope.spec.ts`. Three scenarios cover picker open + bucket selection + reload persistence per [spec.md](spec.md) §US2.

- [X] T019 [US2] Create `frontend/tests/e2e/scope.spec.ts` with per-test seed: `loginAsAdmin(page)` then `seedBucket()` (under the default `primary` backend). No `beforeEach` block — each test does its own seed so they share no state.
- [X] T020 [US2] Implement scenario "picker opens with Backends + Buckets sections" — click the trigger (matched by `aria-label /^Scope picker —/`), assert both section headings render.
- [X] T021 [US2] Implement scenario "selecting a bucket updates the topbar breadcrumb" — open picker, click the seeded bucket row, assert the trigger's `aria-label` embeds the seeded bucket name.
- [X] T022 [US2] Implement scenario "reload preserves the selected scope" — select bucket, `page.reload()`, re-resolve the trigger and assert the bucket name is still in `aria-label`. Catches a regression where `safeRead()` in `ScopeContext` skips the localStorage hydration.
- [~] T023 [US2] Run `pnpm exec playwright test scope.spec.ts` ten times consecutively; zero flake. **DEFERRED to operator runtime** alongside T018 — requires the full stack up. Static gates verified: tsc clean; playwright discovers all 3 scenarios.

**Auxiliary**: `seedBucket()` helper pulled forward from US3's T024 into `fixtures/seed.ts` during this phase — US2 needs at least one bucket present for the picker rows to render.

**Checkpoint**: US2 done. Combined US1+US2 is the operator-critical P1 surface (within the bounds of what the current UI exposes), fully guarded.

---

## Phase 5: User Story 3 — Bucket list & object key open (P2)

**Story goal**: Catch any regression in the most common day-1 operator path: `/buckets → bucket detail → ObjectKeys panel → ObjectKey detail`.

**Independent test**: Run `pnpm exec playwright test buckets.spec.ts`. Three scenarios per [spec.md](spec.md) §US3.

- [X] T024 [US3] Extend `frontend/tests/e2e/fixtures/seed.ts` with `seedObjectKey(opts)` per [contracts/fixtures-api.md](contracts/fixtures-api.md) §"`fixtures/seed.ts`". ObjectKey gets a `e2e/${randomHex(8)}` key path. (Not [P] — modifies the same file as T011, additive but sequential.) **Note**: `seedBucket(opts)` was pulled forward into the Phase 4 (US2) commit because the repurposed scope-switching tests needed at least one bucket. T024 is now smaller than originally specified.
- [X] T025 [US3] Create `frontend/tests/e2e/buckets.spec.ts` with a `beforeEach` that logs in, seeds one tenant, one bucket, and one ObjectKey, capturing all three for assertions.
- [X] T026 [US3] Implement scenario "bucket list shows seeded bucket" in `buckets.spec.ts`: navigate to `/buckets` (scope=seeded tenant), assert a row matching the seeded bucket's `displayName` is visible.
- [X] T027 [US3] Implement scenario "bucket detail opens ObjectKeys panel" in `buckets.spec.ts`: click the bucket row, wait for the detail page, assert the ObjectKeys panel is visible and shows the seeded key's name.
- [X] T028 [US3] Implement scenario "ObjectKey detail renders policy editor" in `buckets.spec.ts`: click the ObjectKey row, assert the canonical resource name string appears AND the Cedar policy editor textarea is visible.
- [~] T029 [US3] Run `pnpm exec playwright test buckets.spec.ts` ten times consecutively; zero flake. **DEFERRED to operator runtime** — requires full stack up. Static gates verified: tsc clean; playwright discovers 3 scenarios.

**Checkpoint**: US3 done. Day-1 operator path covered.

---

## Phase 6: User Story 4 — Capability create + revoke + Idempotency contract (P2)

**Story goal**: Catch (a) regressions in the capability create/revoke UI, AND (b) any regression to the middleware-level Idempotency-Key memoize+replay (FR-008 — the single most important guard for the work that shipped on develop today).

**Independent test**: Run `pnpm exec playwright test capabilities.spec.ts`. Three scenarios per [spec.md](spec.md) §US4, with the double-submit collapse being the critical assertion.

- [ ] T030 [US4] Create `frontend/tests/e2e/capabilities.spec.ts` with `beforeEach` that logs in and seeds one tenant.
- [ ] T031 [US4] Implement scenario "single submit creates one capability" in `capabilities.spec.ts`: navigate to `/capabilities` (scoped), fill the form, submit ONCE, assert exactly one row appears within 2 s.
- [ ] T032 [US4] Implement scenario "double-submit collapses (idempotency contract — FR-008)" in `capabilities.spec.ts` as a **two-layer assertion**, because UI-only double-click testing silently passes when the submit button defensively disables after the first click. Both layers MUST be present:

    **Layer A (UI defensive UX)**: Click submit once, assert the submit button becomes `disabled` within 100 ms (or the form transitions away). This guards against accidental UI double-submit.

    **Layer B (network idempotency)**: Capture the `Idempotency-Key` from the first submit via `page.waitForRequest('**/CreateCapability')`. Then issue a SECOND `CreateCapability` request directly from the page context using the same key — either via `page.evaluate(async (k) => fetch('/api/rpc/admin/...', { method: 'POST', headers: { 'Idempotency-Key': k, ... }, body: ... }), capturedKey)`, or via a test-level Connect client with the captured key in request headers.

    After both layers complete, assert the capabilities list shows **exactly 1 row, not 2**. The two-layer split exists because the network-level assertion is the **only** observable hook for the middleware's reflective replay (`reconstructResponse` in `backend/internal/middleware/idempotency.go`) — without Layer B, setting `RequireOnCreate: false` would NOT make this test fail, defeating the entire purpose.

    Code comments MUST reference FR-008, both layers, and the implementation file.
- [ ] T033 [US4] Implement scenario "revoke flips state without removing row" in `capabilities.spec.ts`: create a capability, click revoke + confirm in the dialog, assert the row stays in the table but its status column shows "revoked".
- [ ] T034 [US4] Run `pnpm exec playwright test capabilities.spec.ts` ten times consecutively; zero flake. Critical for FR-008 — if T032 flakes under retries, the test is wrong (the contract is deterministic).

**Checkpoint**: US4 done. The idempotency layer that shipped today is now guarded by automated regression coverage.

---

## Phase 7: User Story 5 — Tenant restore from trash (P3)

**Story goal**: Catch any regression to the soft-delete + restore round-trip — the foundation of the "oops, I deleted that" recovery flow.

**Independent test**: Run `pnpm exec playwright test trash.spec.ts`. Two scenarios per [spec.md](spec.md) §US5. (The originally-planned "slug collision on restore" scenario was dropped during /speckit-analyze — it's unreachable in the current backend because the slug UNIQUE constraint applies across active AND trashed sets per migration 036. Will be added when the BACKLOG'd "partial UNIQUE" change lands.)

- [ ] T035 [US5] Create `frontend/tests/e2e/trash.spec.ts` with `beforeEach` that logs in and seeds one tenant.
- [ ] T036 [US5] Implement scenario "soft-delete moves tenant to trash" in `trash.spec.ts`: navigate to the seeded tenant's detail page, click delete (provide resource_version), confirm; navigate to `/trash`, assert the tenant appears there AND no longer appears on `/tenants`.
- [ ] T037 [US5] Implement scenario "restore from trash" in `trash.spec.ts`: from `/trash`, click the restore action on the seeded tenant, confirm; assert the tenant reappears on `/tenants` within 2 s AND is removed from `/trash`.
- [ ] T038 [US5] Run `pnpm exec playwright test trash.spec.ts` ten times consecutively; zero flake.

**Checkpoint**: US5 done. The full 5-story spec is shipped.

---

## Phase 8: Polish & Cross-Cutting (BACKLOG hygiene, sign-off verification)

**Purpose**: Documentation, sign-off verification per SC-003 + SC-004, and removal of the closed BACKLOG entry per Constitution III.

- [ ] T039 [P] Create `frontend/tests/e2e/README.md` derived from [quickstart.md](quickstart.md) — operator manual stripped down to the parts needed for daily test-running (prereqs, run, debug, troubleshoot). Cross-link to the spec for design rationale.
- [ ] T040 [P] Verify FR-007 (TypeScript conventions): from `frontend/`, run `pnpm run lint` and `pnpm exec tsc --noEmit`. Both MUST exit zero. If a Playwright-specific eslint rule needs adjusting (e.g. `@typescript-eslint/no-floating-promises` on intentionally-awaited locator chains), add a focused override in `frontend/eslint.config.*` with a justification comment — do NOT silence rules globally.
- [ ] T041 Verify SC-002 + SC-003 (each run <3 min wall-clock AND zero flake across 10 consecutive runs of the FULL suite): from `frontend/`, run `for i in {1..10}; do time pnpm run test:e2e || { echo "FLAKED on run $i"; break; }; done`. All 10 must pass AND each run's `real` time must be <3m00s. If any run fails OR exceeds 3 minutes, fix the offending test before sign-off — do NOT add a retry to mask flake or raise SC-002.
- [ ] T042 Verify SC-004 (regression coverage) by dry-running the five intentional-break scenarios in [quickstart.md](quickstart.md) §"Verifying the regression-coverage promise". Each break must produce a clear named test failure in the expected spec file. Revert each break immediately after verification.
- [ ] T043 Delete the "Frontend Playwright suite" entry from `BACKLOG.md` (the existing Aspirational record) in the SAME commit that closes this feature, per Constitution III ("closed BACKLOG entries are deleted, git history is the audit trail").

**Checkpoint**: Feature signed off. BACKLOG cleaned. Suite is the new regression guard for the operator-facing UI.

---

## Dependencies

```
T001 ──► T002 ──► T003 ──► T006
           │
           ├─► T004 [P] ─┐
           │             ├─► T007 ──► T012 ──► (every US phase) ──► T039 / T040 [P]
           ├─► T005 [P] ─┘                       │
           │                                     ├─► T013–T018 (US1, P1) ─┐
           │                                     ├─► T019–T023 (US2, P1) ─┤
           │                                     ├─► T024–T029 (US3, P2) ─┤
           │                                     ├─► T030–T034 (US4, P2) ─┤
           │                                     └─► T035–T038 (US5, P3) ─┤
           │                                                              ▼
           │                                                            T041 ──► T042 ──► T043
           │
           └─► T008 / T009 / T010 [P] ──► T011 ──► T012
```

**Story-level dependencies**:

- **Phase 1 (T001–T006)**: All Setup tasks complete before Phase 2.
- **Phase 2 (T007–T012)**: All Foundational tasks complete before ANY user-story phase.
- **Phases 3–7 (US1–US5)**: Each story is INDEPENDENT once Phase 2 lands. They MAY be implemented sequentially (recommended for human reviewers — lets each PR be small) or in parallel by different agents.
- **Phase 8**: Runs after every user-story phase completes.

**MVP cut-line**: After Phase 3 (US1) the suite is shippable as a "auth-redirect regression guard" — one test file, one scenario set, all of Constitution III's BACKLOG hygiene works the same way. Subsequent stories layer additional surfaces without rework.

---

## Parallel Execution Opportunities

Within Phase 1 (Setup), tasks T004, T005, T006 are `[P]` after T003 completes (different files, no inter-dependencies).

Within Phase 2 (Foundational), the fixture files split cleanly: T008 (credentials), T009 (unique), T010 (auth) can all be authored in parallel after T007 lands. T011 (seed.ts) depends on T008+T010 (uses the admin credential to fetch the Bearer token) — sequential after both.

Across phases 3–7 (user stories), once Phase 2 completes, ALL FIVE stories can be implemented in parallel **at the spec-file level**. Each story's `*.spec.ts` file is independent. The only shared mutation point is `fixtures/seed.ts`: T024 (additive extension for US3) is sequential after T011, not parallel — see C5 note inside T024.

Phase 8 has two `[P]` tasks (T039 README + T040 lint check) that are independent and can run concurrently after the user-story phases land.

Example: split US1 and US4 between two agents in parallel after Phase 2 lands:

```
# Agent A
T013 → T014 → T015 → T016 → T017 → T018

# Agent B  (in parallel — different spec file)
T030 → T031 → T032 → T033 → T034
```

---

## Implementation Strategy

**MVP first.** Implement Phase 1 + Phase 2 + Phase 3 (US1) end-to-end before starting US2. The MVP slice answers the question "does this entire SDD machinery actually produce running tests?" — if anything is broken in the test-stack composition, fixture helpers, or Playwright config, US1 will surface it with the least cognitive load.

**Incremental delivery.** After MVP, ship each subsequent user story as its own focused PR with a `test(e2e): US<N> — <title>` commit. The single-scope rule from Constitution II means no megacommits — five PRs is the right shape.

**Idempotency-test placement.** Despite being in Phase 6 (US4), the idempotency contract assertion (T032) is the highest-value test in the entire suite from a "guards work that just shipped on develop" perspective. If implementing out-of-priority-order is acceptable, US4 can immediately follow US1 — the foundational tasks support it directly.

**Sign-off ritual.** T040 (lint/tsc), T041 (10× consecutive runs + per-run <3 min), and T042 (regression-coverage dry-run) are the final gates. Skipping any of them leaves a Success Criterion unverified — the feature is not shippable.

---

## Task Count Summary

| Phase | Tasks | Story | Parallel-eligible |
|---|---|---|---|
| 1. Setup | T001–T006 (6) | — | T004, T005, T006 |
| 2. Foundational | T007–T012 (6) | — | T008, T009, T010 |
| 3. US1 (P1) MVP | T013–T018 (6) | US1 | none (single file) |
| 4. US2 (P1) | T019–T023 (5) | US2 | none |
| 5. US3 (P2) | T024–T029 (6) | US3 | none (T024 reverted from [P] per C5) |
| 6. US4 (P2) | T030–T034 (5) | US4 | none |
| 7. US5 (P3) | T035–T038 (4) | US5 | none (slug-collision scenario dropped per C1) |
| 8. Polish | T039–T043 (5) | — | T039, T040 |
| **Total** | **43 tasks** | 5 stories | ~8 [P] |

**Independent test criteria per story** (matches SC-001):

| Story | Independent test |
|---|---|
| US1 | `pnpm exec playwright test auth.spec.ts` passes 10×; breaking `AuthGate.tsx`'s `useEffect` makes it fail. |
| US2 | `pnpm exec playwright test scope.spec.ts` passes 10×; commenting `ScopeContext.setScope` makes it fail. |
| US3 | `pnpm exec playwright test buckets.spec.ts` passes 10×; removing the bucket row click handler makes it fail. |
| US4 | `pnpm exec playwright test capabilities.spec.ts` passes 10×; setting `RequireOnCreate: false` makes T032 fail. |
| US5 | `pnpm exec playwright test trash.spec.ts` passes 10×; commenting `RestoreTenant` button handler makes it fail. |

---

## Format validation

Self-check — every task above strictly follows `- [ ] T<NNN> [P?] [Story?] description with file path`:

- ✅ Checkbox prefix on every task (43/43).
- ✅ Sequential IDs T001–T043 (no gaps, no duplicates — post-analyze renumbering: US5 lost one scenario, Polish gained the lint task, net 43).
- ✅ `[P]` only where the task is genuinely parallel-safe (different file, no incomplete-task dependency). T024 explicitly NOT [P] per C5 remediation.
- ✅ `[USN]` ONLY on tasks under Phase 3–7. Setup/Foundational/Polish carry NO story label.
- ✅ File path explicit in every task (no "create the model" without naming the model and its directory).
