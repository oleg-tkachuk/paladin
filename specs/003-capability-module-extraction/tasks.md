---
description: "Task list for capability module extraction"
---

# Tasks: Capability Module Extraction

**Input**: Design documents from `/specs/003-capability-module-extraction/`

**Prerequisites**: [plan.md](./plan.md), [spec.md](./spec.md), [research.md](./research.md), [data-model.md](./data-model.md), [contracts/module-api.md](./contracts/module-api.md)

**Tests**: **INCLUDED and non-optional.** Constitution Principle I is
NON-NEGOTIABLE, and this feature's whole safety argument rests on tests: the
extraction ships no new behaviour, so the only proof it worked is that the
existing suites still pass on both sides of the new boundary, plus the guards
added here ([R-006](./research.md), [R-007](./research.md)).

**Organization**: phases follow the five-step sequence from
[R-008](./research.md), which was designed so `develop` is green after every
commit. Each step carries exactly one conventional-commit scope (Principle II).

> **Revision (2026-07-23, post-`/speckit-analyze`)** — this list was amended to
> close **five** findings. **G1 (CRITICAL)**: charge atomicity had zero task
> coverage *and* zero test coverage in the repo; T039/T040 add it. **C1
> (HIGH)**: the golden-token test was one commit later than the move it
> guards, violating Principle I; it is now T033, inside the move commit.
> **G2**: generation fencing had no assertion; T044 adds it. **A1**: SC-009's
> latency claim was untestable; T005 now captures a baseline and T064 asserts
> a numeric bound against it. **I1**: the consumer-file count was taken on a
> different branch — T008 now says 28, not 33.
>
> A second analysis pass then corrected documentation drift the first
> remediation introduced (stale task-ID range, an outdated commit enumeration,
> and two Phase 0 statements the growing task list had falsified). Those edits
> landed in `research.md` and `plan.md`; this list was unaffected apart from
> the count above.
>
> A third pass found the same class of drift in `contracts/` and the plan's
> structure tree. A fourth mapped the spec's **edge cases** to tasks for the
> first time and found three with no coverage anywhere — T049/T050/T051 close
> them, taking the list to **65 tasks**.

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
| 4 | 4 | `test(capability)` | add in-memory store, atomicity + edge-case guards, and example |
| — | 5 | `test(capability)` | assert store conformance at compile time |
| 5 | 6 | `docs(capability)` | document and version the module |
| — | 7 | `docs(backlog)` | record deferred decisions |

---

## Phase 1: Setup — baselines before anything moves

**Purpose**: capture the numbers this refactor will be judged against. Doing
this *after* the move would mean comparing against memory instead of evidence
(plan Risks, row 5).

- [ ] T001 [P] Record current coverage of the package under extraction: run `go test ./internal/capability/... -coverprofile` from `backend/` and write per-function output to `specs/003-capability-module-extraction/baseline-coverage.txt`
- [ ] T002 [P] Snapshot the current dependency graph of `backend/internal/capability` (`go list -deps`) into `specs/003-capability-module-extraction/baseline-deps.txt`, so the post-extraction module graph can be diffed against it
- [ ] T003 Generate a token with the **current** code using a fixed test key and check it in as `capability/testdata/golden_token.jwt` (created here, consumed by T033) — it must be produced before any refactor touches the serialisation path
- [ ] T004 [P] Record the current full-suite pass state from `backend/` (`go test ./...`) as the green baseline every later phase must restore
- [ ] T005 [P] Record the p99 latency of `StandardVerifier.Verify` over ≥10 000 iterations into `specs/003-capability-module-extraction/baseline-latency.txt` — the numeric reference SC-009 is asserted against in T064, then commit as `chore(capability): capture pre-extraction baselines`

**Checkpoint**: baselines exist and are checked in; no production file has changed.

---

## Phase 2: Foundational — the only step with semantic risk

**Purpose**: remove the database-driver leak ([R-002](./research.md)) and stand
up the module shell. **Blocks every user story.** Ships as **two** commits, one
scope each.

### Step 1 — parameterise the charge transaction handle (`refactor(capability)`)

⚠️ Lands **alone**, before any file moves, so a bisect isolates it (plan Risks, row 1).

- [ ] T006 Add the type parameter to the usage contract in `backend/internal/capability/usage.go`: change `UsageStore` to `UsageStore[TX any]` and `onCharged func(ctx context.Context, tx pgx.Tx) error` to `onCharged func(ctx context.Context, tx TX) error` (line ~78) — expresses the atomicity contract without naming a database technology (FR-012)
- [ ] T007 Add the matching type parameter to `MeteringStore` in `backend/internal/capability/metering_store.go` (line ~66), so it wraps `UsageStore[TX]` and threads `TX` through unchanged
- [ ] T008 Create `backend/internal/capability/alias.go` declaring `type UsageStore = capability.UsageStore[pgx.Tx]` and `type MeteringStore = capability.MeteringStore[pgx.Tx]` — the containment boundary that keeps all 28 consumers source-compatible
- [ ] T009 Update the relational implementation in `backend/internal/capability/postgres/usage.go` to satisfy the instantiated contract (`UsageStore[pgx.Tx]`)
- [ ] T010 Verify the single production call site in `backend/internal/auth/capability_interceptor.go` (line ~535) compiles with its `onCharged` closure **unchanged** — if it needs edits, the parameterisation is wrong; stop and reassess
- [ ] T011 Run `go build ./...` and `go test ./...` from `backend/`; confirm the T004 baseline is restored with **zero** consumer files edited beyond T008/T009
- [ ] T012 Commit as `refactor(capability): parameterise the charge transaction handle`

**Gate**: if more than the two declaration sites plus the alias required
edits, the ripple exceeded its containment boundary — halt before Step 2.

### Step 2 — module skeleton + standalone CI (`build(capability)`)

⚠️ The standalone CI job is a **required deliverable of this step, not a later polish item** ([R-004](./research.md), plan Risks row 2). Without it, `replace` silently hides a module that cannot build alone — which would defeat the entire feature while appearing to work.

- [ ] T013 Create `capability/go.mod` with module path `github.com/oleg-tkachuk/paladin/capability` and Go 1.26, requiring only `github.com/google/uuid` and `go.opentelemetry.io/otel` (FR-001)
- [ ] T014 Add `require` + `replace github.com/oleg-tkachuk/paladin/capability => ../capability` to `backend/go.mod`
- [ ] T015 Add a CI job that runs `go build ./...` and `go test ./...` **from `capability/`, with no `replace` in effect and no `backend/` checkout on the module path** — this is what actually enforces FR-002/FR-003
- [ ] T016 Add a CI assertion that the module's resolved dependency graph contains no database driver and no object-storage package (compare against T002; satisfies SC-002)
- [ ] T017 Commit as `build(capability): add the module skeleton and standalone CI job`

**Checkpoint**: module exists and is empty; PALADIN still builds; the guard that
makes the rest of the work honest is in place.

---

## Phase 3: User Story 2 — PALADIN keeps working unchanged (Priority: P1)

**Goal**: the primitive lives in the module; PALADIN consumes it through published
contracts only, with no observable behavioural change.

**Independent Test**: run PALADIN's existing capability, authorisation, billing,
and outbox suites unchanged — every assertion must pass untouched (SC-003).

### Step 3 — move the core (`refactor(capability)`)

⚠️ **Near-import-only commit.** The sole permitted non-import addition is the golden-token test (T033), which must ship *with* the move it guards (Principle I, finding C1). Any other non-import diff is a review flag (plan Risks, row 4).

- [ ] T018 [P] [US2] Move `types.go` (Capability, Principal, AgentPrincipal, Caveats, the nine verification sentinels, the five store-side sentinels, unit-code helpers) from `backend/internal/capability/` to `capability/`
- [ ] T019 [P] [US2] Move `signer.go` and `verifier.go` (incl. `KeyResolver` and `StaticKeyResolver`) to `capability/` — one of the three published extension points (FR-004)
- [ ] T020 [P] [US2] Move `issuer.go` and `delegate.go` to `capability/`
- [ ] T021 [P] [US2] Move `cache.go` (revocation cache + generation fencing) to `capability/`
- [ ] T022 [P] [US2] Move `jwks.go` and `keyloader.go` to `capability/`
- [ ] T023 [P] [US2] Move `store.go` (the `Store` contract) to `capability/` — second published extension point (FR-004)
- [ ] T024 [P] [US2] Move `usage.go` and `metering_store.go` (already parameterised in T006/T007) to `capability/` — third published extension point (FR-004)
- [ ] T025 [P] [US2] Move `metrics.go` to `capability/`, confirming it relies on OTel's global no-op default so no collector is required ([R-005](./research.md))
- [ ] T026 [P] [US2] Move the existing suites `jwks_test.go`, `signer_test.go`, `verifier_test.go` to `capability/` — they must pass there **unchanged** ([R-006](./research.md))
- [ ] T027 [US2] Rewrite the import path in the 5 `backend/internal/api/v1/` consumers: `batch/handler.go`, `multipart/handler.go`, `object/handler.go`, `object_tag/handler.go`, `presign/handler.go`
- [ ] T028 [US2] Rewrite the import path in the admin consumers: `backend/internal/api/admin/v1/capabilityh/handler.go`, `backend/internal/api/admin/v1/billingh/handler.go`, `backend/internal/api/connectshim/admin/tenant_budget_server.go`
- [ ] T029 [US2] Rewrite the import path in the hot path `backend/internal/auth/capability_interceptor.go`
- [ ] T030 [US2] Rewrite the import path in the wiring: `backend/internal/app/build_capability.go`, `build_capability_jwks.go`, `build_listeners_admin.go`, `build_listeners_api.go`
- [ ] T031 [US2] Rewrite the import path in `backend/internal/worker/capability_purger.go`, `backend/internal/worker/metrics.go`, and `backend/internal/config/types.go`
- [ ] T032 [US2] Rewrite the import path in the test consumers: `backend/internal/auth/capability_{assert,caveats}_test.go`, `backend/internal/middleware/audit_capability_test.go`, `backend/internal/api/admin/v1/capabilityh/*_test.go`, `backend/internal/api/connectshim/admin/tenant_budget_server_test.go`, `backend/tests/integration/{billing,charge,transactional_outbox}_test.go`
- [ ] T033 [US2] Add the golden-token test in `capability/golden_test.go`, verifying the T003 fixture byte-for-byte so any wire-format drift fails CI (FR-006/SC-004, [R-007](./research.md)) — **must land in this commit**, not a later one, so no commit exists where the format is unguarded
- [ ] T034 [US2] Confirm `backend/internal/capability/postgres/` was **not** moved and still compiles against the module's contracts (FR-015)
- [ ] T035 [US2] Run `go test ./...` from `backend/` and from `capability/`; both green, no assertion changed anywhere (SC-003). The unchanged auth suites are also what evidence FR-010 — budget rejection happens at the authorisation boundary, before handler logic — so a failure here is a FR-010 regression, not just a move defect. This task is also the acceptance evidence for FR-016 (every existing consumer still functions) and FR-017 (no assertion changed)
- [ ] T036 [US2] Diff-review the commit: every hunk must be either a moved file, an import line, or `capability/golden_test.go`
- [ ] T037 [US2] Commit as `refactor(capability): move the primitive into its own module`

**Checkpoint**: US2 delivered, and the wire format is guarded from this commit
onward. US1 is now *functionally* met too — a third party could implement the
contracts themselves — but nothing yet **proves** it.

---

## Phase 4: User Story 1 — third-party integrator adopts the primitive (Priority: P1)

**Goal**: someone with no object storage anywhere can complete an issue →
verify → delegate → revoke cycle using only the module.

**Independent Test**: a consumer module outside `backend/` that depends solely
on `capability/`, implements the contracts in memory, and completes the cycle;
its resolved dependency graph contains no storage or database packages.

### Step 4 — reference store, guards, example (`test(capability)`)

- [ ] T038 [P] [US1] Implement the in-memory reference store in `capability/memstore/memstore.go`, satisfying `Store` and `UsageStore[TX]` per [contracts/module-api.md](./contracts/module-api.md) — including the two-ceiling rule (a rejection by **either** ceiling leaves **both** counters unmutated, FR-013). Proves the contracts are satisfiable without a relational database (FR-005)
- [ ] T039 [US1] Give `memstore` a staging-commit semantic for `Charge`: mutate a scratch copy, run `onCharged`, and publish only on success. Without it the module has **no** implementation capable of demonstrating rollback, and SC-008 stays unverifiable
- [ ] T040 [US1] Add `capability/memstore/atomicity_test.go` asserting that an `onCharged` returning an error leaves **both** the per-capability spend counter and the ledger unchanged (FR-011, SC-008). ⚠️ This property is the ADR-0003 guarantee the whole `TX` parameterisation exists to preserve, and it currently has **no test anywhere in the repository** — verified by `grep -rln onCharged --include="*_test.go"` returning nothing
- [ ] T041 [P] [US1] Add `capability/memstore/memstore_test.go` covering revocation idempotency, cascade revoke, request-limit rejection without mutation, and budget rejection without mutation
- [ ] T042 [P] [US1] Add a delegation-narrowing test asserting `ErrDelegationTooWide` on every widening dimension (ops, resources, requests, budget, expiry) and `ErrUnitCodeMismatch` on cross-unit delegation (FR-008, data-model Caveats invariant)
- [ ] T043 [P] [US1] Add a sentinel-distinguishability test proving all nine verification rejection conditions remain individually matchable via `errors.Is` (FR-007, SC-005)
- [ ] T044 [P] [US1] Add a generation-fencing test: a write authorised against a pre-revocation view must not land after the capability is revoked (FR-009, second clause). Moving `cache.go` relocates this logic; nothing currently asserts it
- [ ] T045 [US1] Write the runnable walkthrough in `capability/example/main.go` mirroring [quickstart.md](./quickstart.md): issue → verify → delegate → revoke against `memstore` (FR-019), demonstrating SC-001 — a consumer with no object storage completing the full cycle
- [ ] T046 [US1] Extend the T015 standalone job to run with network egress denied and no container runtime available, proving SC-006's "no database, no network, no container" claim rather than merely implying it from a clean runner
- [ ] T047 [US1] Verify a `UsageStore[struct{}]` instantiation with `onCharged == nil` works end to end — the supported no-transaction mode for consumers without transactional storage (spec edge case; contracts §1.2)
- [ ] T048 [US1] Compare the module's post-extraction dependency graph against the T002 baseline and confirm removal of the database driver (SC-002)
- [ ] T049 [P] [US1] Add `capability/rotation_test.go`: a token signed under a **retired but still published** key must verify, and must stop verifying once that key is withdrawn via `StaticKeyResolver` (spec edge case 3). `quickstart.md` documents a three-step rotation procedure that nothing currently tests — the withdrawal deadline is max-outstanding-TTL, and getting it wrong invalidates live tokens
- [ ] T050 [P] [US1] Add `capability/forgery_test.go`: a syntactically valid, correctly-signed token whose `Store.Get` returns `ErrNotFound` must be rejected as **forgery**, not surfaced as a missing entity (spec edge case 5; contracts §1.1). Collapsing the two would turn a forged token into a 404 instead of an auth failure
- [ ] T051 [P] [US1] Add `capability/unitcode_test.go` covering `NormaliseUnitCode` and `IsAllowedUnitCode`: `""` → `DefaultUnitCode`, every entry of `AllowedUnitCodes` round-trips, an unknown code errors, and `IsAllowedUnitCode("")` is **false** — the deliberate asymmetry with `NormaliseUnitCode` (spec edge case 7). ⚠️ These are published API (contracts §5) with **zero test coverage anywhere in the repository today**; they gate the empty-means-default rule and the comparison behind `ErrUnitCodeMismatch`
- [ ] T052 [US1] Commit as `test(capability): add in-memory store, atomicity guard, and example`

**Checkpoint**: US1 delivered and **proven**, not merely asserted. Combined
with Phase 3, this is the MVP.

---

## Phase 5: User Story 3 — reference storage implementation stays with PALADIN (Priority: P2)

**Goal**: PALADIN ships a production-grade persistent implementation; a third party
can read it as a worked example without being forced to adopt it.

**Independent Test**: the module's suite passes with no persistent storage
available, while PALADIN's relational store satisfies the same published contracts.

- [ ] T053 [P] [US3] Add compile-time conformance assertions in `backend/internal/capability/postgres/` (`var _ capability.Store = (*Store)(nil)`, `var _ capability.UsageStore[pgx.Tx] = (*UsageStore)(nil)`) so contract drift fails the build rather than a runtime call
- [ ] T054 [US3] Confirm PALADIN supplies its store to the module through published contracts only, with no privileged access unavailable to third parties (FR-014) — review `backend/internal/app/build_capability.go` for any non-contract coupling
- [ ] T055 [US3] Confirm the module suite passes in isolation with no persistent storage present (SC-006 re-verified after Phase 4 additions), then commit as `test(capability): assert store conformance at compile time`

---

## Phase 6: User Story 4 — independent release and adoption signal (Priority: P3)

**Goal**: a prospective adopter can discover, understand, and pin the module
without tracking PALADIN's release cadence.

**Independent Test**: from a clean environment, following only the module's own
documentation reaches a working issue → verify cycle (SC-007).

### Step 5 — documentation and versioning (`docs(capability)`)

- [ ] T056 [US4] Write `capability/README.md` explaining the primitive **without any reference to object storage** (FR-018) — adapt [quickstart.md](./quickstart.md), which was authored to this constraint
- [ ] T057 [P] [US4] Add package-level doc comments to `capability/doc.go` covering the three contracts a consumer implements and the no-transaction mode
- [ ] T058 [US4] Tag the module's first version (`capability/vX.Y.Z` per Go's nested-module tagging convention) and pin it in `backend/go.mod`'s `require`, keeping `replace` for local development (FR-020, [R-004](./research.md))
- [ ] T059 [US4] Validate SC-007 by having the walkthrough followed end to end using **only** `capability/README.md`, with PALADIN's documentation closed
- [ ] T060 [US4] Commit as `docs(capability): document and version the module`

---

## Phase 7: Polish & Cross-Cutting Concerns

- [ ] T061 [P] Add the BACKLOG entry *"Capability module: dedicated repository"* with Status/Reason/Definition of Done/Blockers, recording the import-path branding cost accepted in [R-003](./research.md) (Principle III)
- [ ] T062 [P] Add the BACKLOG entry *"Capability module: publish + version policy"* with the same four fields (Principle III), then commit as `docs(backlog): record capability module deferrals`
- [ ] T063 Compare post-extraction module coverage against the T001 baseline; any regression is either fixed or recorded as a BACKLOG entry — not silently accepted
- [ ] T064 Re-measure `Verify` p99 over ≥10 000 iterations and assert it is within **5%** of the T005 baseline (SC-009). A miss is a stop condition, not a note
- [ ] T065 Confirm every checklist item in [checklists/requirements.md](./checklists/requirements.md) still holds against the delivered result

---

## Dependencies

```
Phase 1 (baselines: coverage, deps, golden fixture, green, latency)
   └─→ Phase 2 Step 1 (parameterise)  ← ONLY step with semantic risk
          └─→ Phase 2 Step 2 (skeleton + standalone CI)
                 └─→ Phase 3 / US2 (move + golden-token test)   ← MVP part 1
                        ├─→ Phase 4 / US1 (memstore, atomicity) ← MVP part 2
                        │      └─→ Phase 5 / US3
                        │             └─→ Phase 6 / US4
                        └─────────────────────────────────────→ Phase 7
```

**Hard ordering constraints**:

- **T003 before every refactor task.** The golden token must be minted by the
  pre-extraction code, or it proves nothing.
- **T005 before T006.** The latency baseline must predate the parameterisation,
  or SC-009 has nothing to compare against.
- **T006–T012 alone in one commit.** Isolating the only risky change is the
  whole point of the sequence ([R-008](./research.md)).
- **T015/T016 before T018.** Moving code before the standalone job exists means
  moving it without the guard that detects a broken module.
- **T033 inside the Phase 3 commit.** Deferring it leaves a commit where the
  wire format is unguarded (Principle I; finding C1).
- **T039 before T040.** Rollback cannot be asserted against a store with no
  rollback semantic.
- **T034 before T035.** Confirm the relational store stayed put before
  declaring the suites green.

**Story independence**: US3 and US4 depend on US1/US2 being complete but are
independent of each other. US1 and US2 are **jointly** the MVP — see below.

---

## Parallel Execution Opportunities

**Phase 1** — T001, T002, T004, T005 are independent reads (T003 must precede any refactor):

```
T001 ‖ T002 ‖ T004 ‖ T005   →   T003
```

**Phase 3** — the file moves T018–T026 touch disjoint files and parallelise fully; the import rewrites T027–T032 also parallelise but must follow the moves:

```
T018 ‖ T019 ‖ T020 ‖ T021 ‖ T022 ‖ T023 ‖ T024 ‖ T025 ‖ T026
   →  T027 ‖ T028 ‖ T029 ‖ T030 ‖ T031 ‖ T032
   →  T033 → T034 → T035 → T036 → T037
```

**Phase 4** — T038 gates everything; T039→T040 is a strict chain; the rest fan out:

```
T038 → T039 → T040
T038 → (T041 ‖ T042 ‖ T043 ‖ T044)
(T049 ‖ T050 ‖ T051)          # edge-case guards — independent of memstore
   →  T045 → (T046 ‖ T047 ‖ T048) → T052
```

**Phase 7** — T061 and T062 are independent file appends.

---

## Implementation Strategy

### MVP scope

**Phases 1–4 (T001–T052).** Unusually, the MVP spans **two** P1 stories rather
than one. This is deliberate and is argued in [spec.md](./spec.md): an
extraction that makes the module importable but regresses PALADIN has not
succeeded, and one that keeps PALADIN green without producing a usable module has
not either. Neither story is a viable slice alone.

Delivering Phases 1–4 yields: a module a third party can adopt, PALADIN unchanged
in behaviour, and executable guards on both claims — including, for the first
time, a test for the charge-atomicity guarantee.

### Incremental delivery

1. **Phases 1–2** — de-risked foundation; nothing has moved yet, and the
   riskiest change is isolated in its own commit.
2. **Phase 3** — the move, with its wire-format guard in the same commit. PALADIN
   green. Stop here and the feature is *technically* complete but unproven.
3. **Phase 4** — the proof. **Ship no earlier than here.**
4. **Phases 5–7** — conformance hardening, adoption enablement, hygiene.

### Stop conditions

Halt and reassess rather than pressing on if:

- T010 requires editing the `onCharged` closure → the parameterisation design
  is wrong.
- T011 shows edits rippling beyond the two declarations plus the alias → the
  containment boundary failed.
- T033 fails → the wire format changed during the move; **do not proceed**,
  live agent credentials are at stake.
- T040 cannot be made to pass → the atomicity guarantee does not hold as
  documented, which is a finding about the *existing* system, not about this
  refactor. Record it and escalate rather than weakening the test.
- T048 still shows a database driver in the graph → FR-003 is unmet and the
  feature's central promise is broken.
- T064 exceeds the 5% bound → investigate before tagging; a silent latency
  regression on the authorisation hot path is not acceptable polish debt.

---

## Task Summary

| Phase | Story | Tasks | Count |
|---|---|---|---:|
| 1 — Setup | — | T001–T005 | 5 |
| 2 — Foundational | — | T006–T017 | 12 |
| 3 — Move | US2 (P1) | T018–T037 | 20 |
| 4 — Prove | US1 (P1) | T038–T052 | 15 |
| 5 — Conformance | US3 (P2) | T053–T055 | 3 |
| 6 — Adoption | US4 (P3) | T056–T060 | 5 |
| 7 — Polish | — | T061–T065 | 5 |
| **Total** | | | **65** |

**Requirement coverage**: 29/29 (20 FR + 9 SC). The two previously uncovered —
FR-011 and SC-008 — are closed by T039/T040; the two partials — FR-009 and
FR-010 — by T044 and the FR-010 note on T035.

**Edge-case coverage**: 8/8. Five were already covered (cross-currency and
widening delegation by T042, in-flight revocation by T044, budget exhaustion
by T040/T041, no-transaction consumer by T047). The remaining three — key
rotation, missing-record-as-forgery, and unset budget unit — are closed by
T049/T050/T051. They were *identified* in the spec from the start but never
mapped to work, which is exactly how a checklist item reading "edge cases are
identified" lets a gap survive three review passes.

**Parallel opportunities**: 25 tasks marked `[P]`, concentrated in the file
moves (Phase 3) and the test additions (Phase 4).

**Commits**: 8, one logical scope each, satisfying Principle II without
inflating any scope to "everything-touched". Every row of the Commit Scope
Map has a corresponding commit task — verify with
`grep -ciE 'commit as \`' tasks.md`.
