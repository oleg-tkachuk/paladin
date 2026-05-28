# Implementation Plan: Frontend Playwright E2E Test Suite

**Branch**: `001-frontend-playwright-e2e` | **Date**: 2026-05-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/001-frontend-playwright-e2e/spec.md`

## Summary

Add a Playwright 1.60.0 (Chromium-only) test suite under
`frontend/tests/e2e/` that exercises the operator-critical UI
journeys end-to-end against a docker-compose stack: Postgres 16
+ PALADIN backend + Next.js frontend in production-mode bundle
(`next start`). The S3 backend (Garage v2.3) is an **external,
cluster-shared** dependency (Clarification Q7) — consumed via
`kubectl port-forward`, not bundled into the compose file. Six+
test cases across five user stories.
Suite is locally runnable only in v1; CI integration is
deferred. Database isolation via shared Postgres +
UUID-suffixed per-test fixtures. Seeded admin uses a fixed
dev-only credential carrying a `NEVER true in prod` comment
per Constitution V. Idempotency contract assertion
(double-submit collapses) is the most important regression
guard — captured as a dedicated test in US4.

## Technical Context

**Language/Version**: TypeScript 6.x (matches frontend's
`tsconfig.json` strict-mode settings).

**Primary Dependencies**:
  - `@playwright/test` `^1.60.0` — test runner + assertion API
  - `@connectrpc/connect` `^2.1.1` + `@connectrpc/connect-web`
    `^2.1.1` — already in frontend, reused for fixture-seeding
    via real RPCs
  - Generated client stubs from `frontend/src/gen/` — also
    reused; the test fixtures call the same generated clients
    the application uses

**Package manager**: `pnpm@^11.3.0` (Clarification Q6 + R-008).
This feature ships the `frontend/` migration from npm to pnpm
in the same commit train as the Playwright dep adoption —
T001 + T001a perform the lockfile migration before
`@playwright/test` is installed. Backend tooling is
unaffected.

**Storage**: Postgres 16 (ephemeral per docker-compose run).
Shared across all tests; per-test fixtures distinguish via
UUID-suffixed identifiers. Garage `dxflrs/garage:v2.3.x` as
the S3-compatible blob backend — deployed externally by
gitops and consumed via port-forward (Clarification Q7); the
compose file requires `PALADIN_E2E_S3_ACCESS_KEY/_SECRET_KEY` via a
`?:` fail-fast gate rather than running its own Garage container.

**Testing**: Playwright. `frontend/tests/e2e/` for tests,
`frontend/tests/e2e/fixtures/` for seed helpers,
`frontend/tests/e2e/test-results/` for failure artifacts.

**Target Platform**: Developer-class machine (M1+/Ryzen 5+,
16 GB RAM). Linux/macOS. No Windows host validation.

**Project Type**: web-application (extends the existing
backend + frontend monorepo; no new top-level module).

**Performance Goals**: Full suite run <3 minutes wall-clock,
including docker-compose `up` cold start. Individual test
budget <30 seconds. Suite parallelism: Playwright default
(workers = CPU/2, capped at 4) — verified to fit budget by
the 6-test count.

**Constraints**:
  - Locally runnable only (FR-006); CI integration deferred.
  - Zero flaky tests across 10 consecutive local runs (SC-003).
  - Tests MUST NOT bypass UI via API shortcuts — login goes
    through the real `/login` form (FR-003); seed data MAY use
    Connect-RPC because it's setup, not verification.
  - Garage's missing S3 features (versioning, tagging) MUST
    NOT be hit — confirmed safe because PALADIN tracks those in
    Postgres tables (`object_versions`, `object_tags`).

**Scale/Scope**: 6+ test cases across 5 user stories. ~500
lines of test code. ~100 lines of fixture helpers.
docker-compose.test.yaml ~80 lines.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after
Phase 1 design.*

| # | Principle | Gate | Status |
|---|---|---|---|
| I | Tests-First Guard Rails | This entire feature IS the tests. Production code touched is zero (test stack only). | ✅ Pass |
| II | Single-Scope Conventional Commits | Plan splits implementation into commits scoped to `test`, `build`, and one `docs` for BACKLOG removal. No multi-scope commits. | ✅ Pass |
| III | BACKLOG Source of Truth | BACKLOG.md entry "Frontend Playwright suite" will be removed in the closing commit per SC-005. | ✅ Pass |
| IV | Pre-1.0 Breaking Allowed | No proto/SQL/API changes. No migrations. | ✅ N/A |
| V | Local-Dev Parity Through Overlays | Test stack is intentionally SEPARATE from `gitops` overlays — it's a docker-compose, not a Helm overlay. `NEVER true in prod` comment carried on the seeded credential per Q4 decision. | ✅ Pass |
| VI | Security Floor (Cedar + JWT + Capability) | Tests EXERCISE the full Cedar+JWT chain via real `/login` (FR-003). No new RPCs created → no new Cedar gates needed. | ✅ Pass |
| VII | Idempotency for Mutations | Tests EXERCISE the contract via US4's double-submit assertion (FR-008). No new Create* RPCs created → no new enforcement gates needed. | ✅ Pass |

**Re-check after Phase 1 design** (below): see "Post-Design
Re-Check" section before the Complexity Tracking table.

## Project Structure

### Documentation (this feature)

```text
specs/001-frontend-playwright-e2e/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output (/speckit-plan command)
├── data-model.md        # Phase 1 output (/speckit-plan command)
├── quickstart.md        # Phase 1 output (/speckit-plan command)
├── contracts/           # Phase 1 output (/speckit-plan command)
│   └── fixtures-api.md  # The seedTenant() / seedBucket() / etc. surface
├── checklists/
│   └── requirements.md  # Spec quality gate (already produced)
└── tasks.md             # Phase 2 output (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
frontend/
├── playwright.config.ts                     # NEW — Playwright config (chromium-only,
│                                              base URL, reporter, retry policy)
├── package.json                             # MODIFIED — add @playwright/test devDep + scripts
└── tests/
    └── e2e/                                  # NEW (root of E2E suite)
        ├── fixtures/
        │   ├── credentials.ts                # seeded admin user, NEVER-in-prod banner
        │   ├── seed.ts                       # seedTenant(), seedBucket(), seedCapability()
        │   ├── auth.ts                       # loginAsAdmin() page-driving helper
        │   └── unique.ts                     # uniqueSlug(), uniqueDisplayName() — UUID-suffixed
        ├── auth.spec.ts                      # US1 — login + AuthGate redirect
        ├── scope.spec.ts                     # US2 — backend + bucket scope switching
        ├── buckets.spec.ts                   # US3 — bucket list & object key open
        ├── capabilities.spec.ts              # US4 — capability create + revoke + idempotency
        ├── trash.spec.ts                     # US5 — tenant restore from trash
        ├── docker-compose.test.yaml          # NEW — Postgres + PALADIN + Garage + frontend
        ├── README.md                         # NEW — how to run, troubleshoot, add a test
        └── test-results/                     # generated; gitignored
            └── (screenshots, videos, traces from failures)

backend/                                       # NO BACKEND CODE CHANGES
gitops/                                      # NO INFRA CHANGES (test stack is compose-only)
```

**Structure Decision**: Web application (Option 2 from the
template). The PALADIN repo already has the `backend/` +
`frontend/` split. All test infrastructure lives under
`frontend/tests/e2e/` because that's the package whose
quality the suite guards. `docker-compose.test.yaml`
co-locates with the tests rather than under `backend/deploy/`
to keep the test rig self-contained — one directory, one
README, no cross-package coupling.

## Phase 0: Outline & Research

See [research.md](research.md). Highlights:
  - Playwright 1.60.0 confirmed latest stable via
    `npm view @playwright/test version` (npm CLI used only
    for the registry query — the project uses pnpm).
  - pnpm 11.3.0 confirmed latest stable via
    `npm view pnpm version`.
  - Garage v2.3.0+ confirmed via official docs; supports every
    S3 op PALADIN backend actually calls (Sigv4, presigned URLs,
    multipart upload). Tag/version S3 ops are missing but
    PALADIN tracks those in Postgres tables.
  - PALADIN backend per-plane container layout reused from
    existing `backend/deploy/docker-compose.yaml`.
  - All 5 NEEDS CLARIFICATION items from the spec resolved in
    /speckit-clarify.

## Phase 1: Design & Contracts

Produced:
  - [data-model.md](data-model.md) — entities + relationships
    in the test stack.
  - [contracts/fixtures-api.md](contracts/fixtures-api.md) —
    the seed helper signatures the tests rely on.
  - [quickstart.md](quickstart.md) — one-page operator manual.

### Agent context update

`CLAUDE.md` updated between the `<!-- SPECKIT START -->` and
`<!-- SPECKIT END -->` markers to point at this plan
(`specs/001-frontend-playwright-e2e/plan.md`).

## Post-Design Re-Check

Re-running the 7-row Constitution Check against the produced
design (data-model, contracts, quickstart):

| # | Principle | Verdict |
|---|---|---|
| I | Tests-First | Design entirely IS test code + a test-stack docker-compose. Production code untouched. ✅ |
| II | Single-Scope Commits | tasks.md will sequence: (a) `build(frontend)` for Playwright dep + scripts, (b) `build(test-stack)` for docker-compose, (c) `test(e2e)` per user story (US1→US5), (d) `docs(backlog)` to delete the entry. ✅ |
| III | BACKLOG Source of Truth | Closing commit deletes the entry. ✅ |
| IV | Pre-1.0 Breaking Allowed | No proto/SQL/API changes in this feature. N/A. ✅ |
| V | Local-Dev Parity | Test stack is compose-only by design. Seeded credential carries NEVER-in-prod comment in `credentials.ts`. ✅ |
| VI | Security Floor | Test driver USES `/login` form → exercises full JWT+Cedar+capability chain. No bypass. ✅ |
| VII | Idempotency | US4 explicitly asserts double-submit collapse. Frontend transport already auto-injects key. ✅ |

**No violations. No complexity-tracking entries needed.**

## Complexity Tracking

> Empty — Constitution Check passes cleanly on both pre-design
> and post-design re-checks. No principle requires a
> documented justification.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| _(none)_ | — | — |
