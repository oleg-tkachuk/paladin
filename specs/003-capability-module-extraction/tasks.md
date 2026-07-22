---
description: "Task list for capability module extraction"
---

# Tasks: Capability Module Extraction

**Input**: Design documents from `/specs/003-capability-module-extraction/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/module-api.md](./contracts/module-api.md)

**Tests**: **INCLUDED and non-optional.** Constitution Principle I is
NON-NEGOTIABLE, and this feature's whole safety argument rests on tests: the
extraction ships no new behaviour, so the only proof it worked is that the
existing suites still pass on both sides of the new boundary, plus three new
guards ([R-006](./research.md), [R-007](./research.md)).

**Organization**: phases follow the five-step sequence from
[R-008](./research.md), which was designed so `develop` is green after every
commit. Each step carries exactly one conventional-commit scope (Principle II).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: parallelisable — different files, no dependency on an incomplete task
- **[Story]**: `[US1]`–`[US4]` from [spec.md](./spec.md); Setup/Foundational/Polish carry none

## Path Conventions

Per [plan.md](./plan.md#source-code-repository-root): new module at repo-root
`capability/`; existing service at `backend/`. All paths below are
repo-relative.

## Commit Scope Map (Principle II)

| Step | Phase | Scope | Subject |
|---|---|---|---|
| — | 1 | `chore(capability)` | capture pre-extraction baselines |
| 1 | 2 | `refactor(capability)` | parameterise the charge transaction handle |
| 2 | 2 | `build(capability)` | add the module skeleton and standalone CI job |
| 3 | 3 | `refactor(capability)` | move the primitive into its own module |
| 4 | 4 | `test(capability)` | add in-memory store, golden token, and example |
| 5 | 6 | `docs(capability)` | document and version the module |

---

## Phase 1: Setup — baselines before anything moves

**Purpose**: capture the numbers this refactor will be judged against. Doing
this *after* the move would mean comparing against memory instead of evidence
(plan Risks, row 5).

- [ ] T001 [P] Record the current coverage of the package under extraction: run `go test ./internal/capability/... -coverprofile` from `backend/` and write the per-function output to `specs/003-capability-module-extraction/baseline-coverage.txt`
- [ ] T002 [P] Snapshot the current dependency graph of `backend/internal/capability` (`go list -deps`) into `specs/003-capability-module-extraction/baseline-deps.txt`, so the post-extraction module graph can be diffed against it
- [ ] T003 Generate a token with the **current** code using a fixed test key and check it in as `capability/testdata/golden_token.jwt` (created here, consumed by T037) — it must be produced before any refactor touches the serialisation path
- [ ] T004 [P] Record the current full-suite pass state from `backend/` (`go test ./...`) as the green baseline every later phase must restore

**Checkpoint**: baselines exist; no production file has changed.

---

## Phase 2: Foundational — the only step with semantic risk

**Purpose**: remove the database-driver leak ([R-002](./research.md)) and stand
up the module shell. **Blocks every user story.** Ships as **two** commits, one
scope each.

### Step 1 — parameterise the charge transaction handle (`refactor(capability)`)

⚠️ Lands **alone**, before any file moves, so a bisect isolates it (plan Risks, row 1).

- [ ] T005 Add the type parameter to the usage contract in `backend/internal/capability/usage.go`: change `UsageStore` to `UsageStore[TX any]` and `onCharged func(ctx context.Context, tx pgx.Tx) error` to `onCharged func(ctx context.Context, tx TX) error` (line ~78)
- [ ] T006 Add the matching type parameter to `MeteringStore` in `backend/internal/capability/metering_store.go` (line ~66), so it wraps `UsageStore[TX]` and threads `TX` through unchanged
- [ ] T007 Create `backend/internal/capability/alias.go` declaring `type UsageStore = capability.UsageStore[pgx.Tx]` and `type MeteringStore = capability.MeteringStore[pgx.Tx]` — the containment boundary that keeps all 33 consumers source-compatible
- [ ] T008 Update the relational implementation in `backend/internal/capability/postgres/usage.go` to satisfy the instantiated contract (`UsageStore[pgx.Tx]`)
- [ ] T009 Verify the single production call site in `backend/internal/auth/capability_interceptor.go` (line ~535) compiles with its `onCharged` closure **unchanged** — if it needs edits, the parameterisation is wrong; stop and reassess
- [ ] T010 Run `go build ./...` and `go test ./...` from `backend/`; confirm the T004 baseline is restored with **zero** consumer files edited beyond T007/T008
- [ ] T011 Commit as `refactor(capability): parameterise the charge transaction handle`

**Gate**: if more than the two declaration sites plus the alias required
edits, the ripple exceeded its containment boundary — halt before Step 2.

### Step 2 — module skeleton + standalone CI (`build(capability)`)

⚠️ The standalone CI job is a **required deliverable of this step, not a later polish item** ([R-004](./research.md), plan Risks row 2). Without it, `replace` silently hides a module that cannot build alone — which would defeat the entire feature while appearing to work.

- [ ] T012 Create `capability/go.mod` with module path `github.com/oleg-tkachuk/paladin/capability` and Go 1.26, requiring only `github.com/google/uuid` and `go.opentelemetry.io/otel`
- [ ] T013 Add `require` + `replace github.com/oleg-tkachuk/paladin/capability => ../capability` to `backend/go.mod`
- [ ] T014 Add a CI job that builds and tests the module **from `capability/` with no `replace` in effect** — this is what actually enforces FR-002/FR-003, and it must fail if a driver or storage SDK enters the graph
- [ ] T015 Add a CI assertion that the module's resolved dependency graph contains no database driver and no object-storage package (compare against T002; satisfies SC-002)
- [ ] T016 Commit as `build(capability): add the module skeleton and standalone CI job`

**Checkpoint**: module exists and is empty; PALADIN still builds; the guard that
makes the rest of the work honest is in place.

---

## Phase 3: User Story 2 — PALADIN keeps working unchanged (Priority: P1)

**Goal**: the primitive lives in the module; PALADIN consumes it through published
contracts only, with no observable behavioural change.

**Independent Test**: run PALADIN's existing capability, authorisation, billing,
and outbox suites unchanged — every assertion must pass untouched (SC-003).

### Step 3 — move the core (`refactor(capability)`)

⚠️ **Import-only commit.** Any non-import diff here is a review flag (plan Risks, row 4).

- [ ] T017 [P] [US2] Move `types.go` (Capability, Principal, AgentPrincipal, Caveats, the nine sentinels, unit-code helpers) from `backend/internal/capability/` to `capability/`
- [ ] T018 [P] [US2] Move `signer.go` and `verifier.go` to `capability/`
- [ ] T019 [P] [US2] Move `issuer.go` and `delegate.go` to `capability/`
- [ ] T020 [P] [US2] Move `cache.go` (revocation cache + generation fencing) to `capability/`
- [ ] T021 [P] [US2] Move `jwks.go` and `keyloader.go` to `capability/`
- [ ] T022 [P] [US2] Move `store.go` (the `Store` contract) to `capability/`
- [ ] T023 [P] [US2] Move `usage.go` and `metering_store.go` (already parameterised in T005/T006) to `capability/`
- [ ] T024 [P] [US2] Move `metrics.go` to `capability/`, confirming it relies on OTel's global no-op default so no collector is required ([R-005](./research.md))
- [ ] T025 [P] [US2] Move the existing suites `jwks_test.go`, `signer_test.go`, `verifier_test.go` to `capability/` — they must pass there **unchanged** ([R-006](./research.md))
- [ ] T026 [US2] Rewrite the import path in the 5 `backend/internal/api/v1/` consumers: `batch/handler.go`, `multipart/handler.go`, `object/handler.go`, `object_tag/handler.go`, `presign/handler.go`
- [ ] T027 [US2] Rewrite the import path in the admin consumers: `backend/internal/api/admin/v1/capabilityh/handler.go`, `backend/internal/api/admin/v1/billingh/handler.go`, `backend/internal/api/connectshim/admin/tenant_budget_server.go`
- [ ] T028 [US2] Rewrite the import path in the hot path `backend/internal/auth/capability_interceptor.go`
- [ ] T029 [US2] Rewrite the import path in the wiring: `backend/internal/app/build_capability.go`, `build_capability_jwks.go`, `build_listeners_admin.go`, `build_listeners_api.go`
- [ ] T030 [US2] Rewrite the import path in `backend/internal/worker/capability_purger.go`, `backend/internal/worker/metrics.go`, and `backend/internal/config/types.go`
- [ ] T031 [US2] Rewrite the import path in the test consumers: `backend/internal/auth/capability_{assert,caveats}_test.go`, `backend/internal/middleware/audit_capability_test.go`, `backend/internal/api/admin/v1/capabilityh/*_test.go`, `backend/tests/integration/{billing,charge,transactional_outbox}_test.go`
- [ ] T032 [US2] Confirm `backend/internal/capability/postgres/` was **not** moved and still compiles against the module's contracts (FR-015)
- [ ] T033 [US2] Run `go test ./...` from `backend/` and from `capability/`; both green, no assertion changed anywhere (SC-003)
- [ ] T034 [US2] Diff-review the commit: every hunk outside the moved files must be an import line only
- [ ] T035 [US2] Commit as `refactor(capability): move the primitive into its own module`

**Checkpoint**: US2 delivered. US1 is now *functionally* met too — a third
party could implement the contracts themselves — but nothing yet **proves** it.

---

## Phase 4: User Story 1 — third-party integrator adopts the primitive (Priority: P1)

**Goal**: someone with no object storage anywhere can complete an issue →
verify → delegate → revoke cycle using only the module.

**Independent Test**: a consumer module outside `backend/` that depends solely
on `capability/`, implements the contracts in memory, and completes the cycle;
its resolved dependency graph contains no storage or database packages.

### Step 4 — reference store, guards, example (`test(capability)`)

- [ ] T036 [P] [US1] Implement the in-memory reference store in `capability/memstore/memstore.go`, satisfying `Store` and `UsageStore[TX]` per [contracts/module-api.md](./contracts/module-api.md) — including the two-ceiling rule (a rejection by **either** ceiling leaves **both** counters unmutated)
- [ ] T037 [US1] Add the golden-token test in `capability/golden_test.go`, verifying the T003 fixture byte-for-byte so any wire-format drift fails CI ([R-007](./research.md), FR-006/SC-004)
- [ ] T038 [P] [US1] Add `capability/memstore/memstore_test.go` covering revocation idempotency, cascade revoke, request-limit rejection without mutation, and budget rejection without mutation
- [ ] T039 [P] [US1] Add a delegation-narrowing test asserting `ErrDelegationTooWide` on every widening dimension (ops, resources, requests, budget, expiry) and `ErrUnitCodeMismatch` on cross-unit delegation (FR-008, data-model Caveats invariant)
- [ ] T040 [P] [US1] Add a sentinel-distinguishability test proving all nine rejection conditions remain individually matchable via `errors.Is` (FR-007, SC-005)
- [ ] T041 [US1] Write the runnable walkthrough in `capability/example/main.go` mirroring [quickstart.md](./quickstart.md): issue → verify → delegate → revoke against `memstore` (FR-019)
- [ ] T042 [US1] Add a CI check that the module suite passes with **no database, no network, no container** (SC-006)
- [ ] T043 [US1] Verify a `UsageStore[struct{}]` instantiation with `onCharged == nil` works end to end — the supported no-transaction mode for consumers without transactional storage (spec edge case; contracts §1.2)
- [ ] T044 [US1] Compare the module's post-extraction dependency graph against the T002 baseline and confirm removal of the database driver (SC-002)
- [ ] T045 [US1] Commit as `test(capability): add in-memory store, golden token, and example`

**Checkpoint**: US1 delivered and **proven**, not merely asserted. Combined
with Phase 3, this is the MVP.

---

## Phase 5: User Story 3 — reference storage implementation stays with PALADIN (Priority: P2)

**Goal**: PALADIN ships a production-grade persistent implementation; a third party
can read it as a worked example without being forced to adopt it.

**Independent Test**: the module's suite passes with no persistent storage
available, while PALADIN's relational store satisfies the same published contracts.

- [ ] T046 [P] [US3] Add a compile-time conformance assertion in `backend/internal/capability/postgres/` (`var _ capability.Store = (*Store)(nil)`, `var _ capability.UsageStore[pgx.Tx] = (*UsageStore)(nil)`) so contract drift fails the build rather than a runtime call
- [ ] T047 [US3] Confirm PALADIN supplies its store to the module through published contracts only, with no privileged access unavailable to third parties (FR-014) — review `backend/internal/app/build_capability.go` for any non-contract coupling
- [ ] T048 [P] [US3] Confirm the module suite passes in isolation with no persistent storage present (SC-006 re-verified after Phase 4 additions)

**Checkpoint**: the boundary is proven in both directions — module works
without the store, store conforms without special access.

---

## Phase 6: User Story 4 — independent release and adoption signal (Priority: P3)

**Goal**: a prospective adopter can discover, understand, and pin the module
without tracking PALADIN's release cadence.

**Independent Test**: from a clean environment, following only the module's own
documentation reaches a working issue → verify cycle (SC-007).

### Step 5 — documentation and versioning (`docs(capability)`)

- [ ] T049 [US4] Write `capability/README.md` explaining the primitive **without any reference to object storage** (FR-018) — adapt [quickstart.md](./quickstart.md), which was authored to this constraint
- [ ] T050 [P] [US4] Add package-level doc comments to `capability/doc.go` covering the three contracts a consumer implements and the no-transaction mode
- [ ] T051 [US4] Tag the module's first version (`capability/vX.Y.Z` per Go's nested-module tagging convention) and pin it in `backend/go.mod`'s `require`, keeping `replace` for local development (FR-020, [R-004](./research.md))
- [ ] T052 [US4] Validate SC-007 by having the walkthrough followed end to end using **only** `capability/README.md`, with PALADIN's documentation closed
- [ ] T053 [US4] Commit as `docs(capability): document and version the module`

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T054 [P] Add the BACKLOG entry *"Capability module: dedicated repository"* with Status/Reason/Definition of Done/Blockers, recording the import-path branding cost accepted in [R-003](./research.md) (Principle III)
- [ ] T055 [P] Add the BACKLOG entry *"Capability module: publish + version policy"* with the same four fields (Principle III)
- [ ] T056 Compare post-extraction module coverage against the T001 baseline; any regression is either fixed or recorded as a BACKLOG entry — not silently accepted
- [ ] T057 Measure the request-path authorisation latency against the pre-extraction baseline and confirm no measurable regression (SC-009)
- [ ] T058 Confirm every checklist item in [checklists/requirements.md](./checklists/requirements.md) still holds against the delivered result

---

## Dependencies

```
Phase 1 (baselines)
   └─→ Phase 2 Step 1 (parameterise)  ← ONLY step with semantic risk
          └─→ Phase 2 Step 2 (skeleton + standalone CI)
                 └─→ Phase 3 / US2 (move)          ← MVP part 1
                        ├─→ Phase 4 / US1 (prove)  ← MVP part 2
                        │      └─→ Phase 5 / US3
                        │             └─→ Phase 6 / US4
                        └─────────────────────────────→ Phase 7
```

**Hard ordering constraints**:

- **T003 before every refactor task.** The golden token must be minted by the
  pre-extraction code, or it proves nothing.
- **T005–T011 alone in one commit.** Isolating the only risky change is the
  whole point of the sequence ([R-008](./research.md)).
- **T014/T015 before T017.** Moving code before the standalone job exists means
  moving it without the guard that detects a broken module.
- **T032 before T033.** Confirm the relational store stayed put before
  declaring the suites green.

**Story independence**: US3 and US4 depend on US1/US2 being complete but are
independent of each other. US1 and US2 are **jointly** the MVP — see below.

---

## Parallel Execution Opportunities

**Phase 1** — T001, T002, T004 are independent reads (T003 must precede any refactor):

```
T001 ‖ T002 ‖ T004   →   T003
```

**Phase 3** — the file moves T017–T025 touch disjoint files and parallelise fully; the import rewrites T026–T031 are grouped by consumer cluster and also parallelise, but must follow the moves:

```
T017 ‖ T018 ‖ T019 ‖ T020 ‖ T021 ‖ T022 ‖ T023 ‖ T024 ‖ T025
   →  T026 ‖ T027 ‖ T028 ‖ T029 ‖ T030 ‖ T031
   →  T032 → T033 → T034 → T035
```

**Phase 4** — T036 gates the tests that use it; T037–T040 then parallelise:

```
T036 → (T037 ‖ T038 ‖ T039 ‖ T040) → T041 → T042 ‖ T043 ‖ T044 → T045
```

**Phase 7** — T054 and T055 are independent file appends.

---

## Implementation Strategy

### MVP scope

**Phases 1–4 (T001–T045).** Unusually, the MVP spans **two** P1 stories rather
than one. This is deliberate and is argued in [spec.md](./spec.md): an
extraction that makes the module importable but regresses PALADIN has not
succeeded, and one that keeps PALADIN green without producing a usable module has
not either. Neither story is a viable slice alone.

Delivering Phases 1–4 yields: a module a third party can adopt, PALADIN unchanged
in behaviour, and executable guards on both claims.

### Incremental delivery

1. **Phases 1–2** — de-risked foundation; nothing has moved yet, and the
   riskiest change is isolated in its own commit.
2. **Phase 3** — the move. PALADIN green. Stop here and the feature is *technically*
   complete but unproven.
3. **Phase 4** — the proof. **Ship no earlier than here.**
4. **Phases 5–7** — conformance hardening, adoption enablement, hygiene.

### Stop conditions

Halt and reassess rather than pressing on if:

- T009 requires editing the `onCharged` closure → the parameterisation design
  is wrong.
- T010 shows edits rippling beyond the two declarations plus the alias → the
  containment boundary failed.
- T037 fails → the wire format changed during the move; **do not proceed**,
  live agent credentials are at stake.
- T044 still shows a database driver in the graph → FR-003 is unmet and the
  feature's central promise is broken.

---

## Task Summary

| Phase | Story | Tasks | Count |
|---|---|---|---:|
| 1 — Setup | — | T001–T004 | 4 |
| 2 — Foundational | — | T005–T016 | 12 |
| 3 — Move | US2 (P1) | T017–T035 | 19 |
| 4 — Prove | US1 (P1) | T036–T045 | 10 |
| 5 — Conformance | US3 (P2) | T046–T048 | 3 |
| 6 — Adoption | US4 (P3) | T049–T053 | 5 |
| 7 — Polish | — | T054–T058 | 5 |
| **Total** | | | **58** |

**Parallel opportunities**: 21 tasks marked `[P]`, concentrated in the file
moves (Phase 3) and the test additions (Phase 4).

**Commits**: 6 (one per scope in the Commit Scope Map), satisfying Principle
II's one-logical-scope rule without inflating any scope to "everything-touched".
