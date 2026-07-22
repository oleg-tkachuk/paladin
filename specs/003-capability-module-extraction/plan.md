# Implementation Plan: Capability Module Extraction

**Branch**: `003-capability-module-extraction` | **Date**: 2026-07-23 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `/specs/003-capability-module-extraction/spec.md`

## Summary

Extract PALADIN's capability authorisation primitive — short-lived, signed,
delegable, individually revocable tokens carrying budget caveats and per-call
attribution — into an independently importable Go module, with PALADIN remaining
its reference consumer.

A code survey ([R-001](./research.md)) established that the package already
imports **zero** PALADIN-internal code. This is therefore a packaging exercise
with exactly one piece of real design work: the usage contract names `pgx.Tx`
in its `onCharged` callback, which would force a database driver onto every
consumer. That is resolved by parameterising the transaction handle as a
generic type argument ([R-002](./research.md)), absorbed on PALADIN's side by a
one-line alias so all 28 consumer files (16 production, 12 test) compile
unchanged.

Everything else is mechanical relocation, sequenced ([R-008](./research.md))
so `develop` stays green at every commit.

## Technical Context

**Language/Version**: Go 1.26 (matches `backend/deploy/Dockerfile` `ARG GO_VERSION`)

**Primary Dependencies**: module depends on `github.com/google/uuid` and
`go.opentelemetry.io/otel` only — **no database driver, no storage SDK**

**Storage**: none in the module (contracts only); the relational reference
implementation stays in PALADIN at `backend/internal/capability/postgres/`

**Testing**: `go test`; relocated unit suites + in-memory reference store with a
staging-commit semantic; three guards for properties nothing currently asserts
— golden-token (wire format), induced-failure rollback (charge atomicity),
generation fencing; standalone module CI job that builds with no PALADIN checkout

**Target Platform**: any Go-supported platform — library, no runtime assumptions

**Project Type**: library (nested module) alongside the existing web service

**Performance Goals**: token verification ≤5% slower at p99 than the
pre-extraction baseline over ≥10 000 iterations (SC-009); verification remains
local — no issuer round trip

**Constraints**: token wire format frozen (FR-006); module test suite runs with
no database, no network, no container (SC-006)

**Scale/Scope**: ~2 700 LOC relocated, 2 signatures parameterised, 856 LOC of
relational implementation deliberately left behind, 28 consumer files preserved
(16 production, 12 test — counted on this branch; see [I1 note](#counting-note))

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design — see [Post-Design Re-check](#post-design-re-check).*

| # | Principle | Gate | Status |
|---|---|---|---|
| I | Tests-First Guard Rails | Plan lists new unit + integration tests shipping in the SAME commits as production code. | **Pass** — the extraction adds no behaviour, so the guard rail is that *existing* suites pass on both sides. New tests are tied to the step they guard: the **golden-token test ships in step 3, in the same commit as the move it protects** (an earlier draft deferred it to step 4, which would have left one commit where the wire format was unguarded — corrected after analysis finding C1); `memstore`, the atomicity-rollback test, and the fencing test in step 4; the standalone build/test job in step 2. The `onCharged` parameterisation (step 1) ships with its call-site test updated in the same commit. |
| II | Single-Scope Conventional Commits | One logical scope per commit; multi-scope sweeps split. | **Pass** — [R-008](./research.md) defines five *steps*, which `tasks.md` realises as **eight commits**, each carrying one logical scope: `chore(capability)` (baselines) → `refactor(capability)` (parameterise) → `build(capability)` (skeleton) → `refactor(capability)` (move) → `test(capability)` (guards) → `test(capability)` (conformance) → `docs(capability)` (release) → `docs(backlog)` (deferrals). Verified mechanically: the Commit Scope Map has one commit task per row (8 = 8). The import rewrite across 28 files is one mechanical scope, not a sweep of unrelated edits. |
| III | BACKLOG Source of Truth | Deferred work documented with Status/Reason/DoD/Blockers; closed entries deleted in the closing commit. | **Pass** — two entries to add: *"Capability module: dedicated repository"* (the import-path branding cost accepted in [R-003](./research.md)) and *"Capability module: publish + version policy"*. Both carry the four required fields. No existing entry is closed by this work. |
| IV | Pre-1.0 Breaking Allowed | Breaking proto/SQL/API changes update dependent BACKLOG entries; new SQL migrations have working `-- +goose Down`. | **Pass** — **no SQL migration, no proto change**. The source-level break (two signatures gain a type parameter) is permitted pre-1.0 and is absorbed by an alias. The token **wire format is explicitly frozen** (FR-006) and pinned by a golden fixture ([R-007](./research.md)) — the deliberate exception explained in spec Assumptions. |
| V | Local-Dev Parity Through Overlays | New chart values delivered via `gitops` overlay files, not inline `helm.values`. | **N/A** — no chart value, no deployment surface, no runtime configuration change. Pure source reorganisation. |
| VI | Security Floor (Cedar + JWT + Capability) | Every new RPC passes JWT audience pinning, Cedar authorize, capability caveats. | **N/A for new surface** (no new RPC). **Pass on preservation**: this feature touches gate (3) of the security floor directly, so FR-007/FR-008/FR-009 require all nine rejection conditions, the over-wide-delegation rule, and revocation fencing to remain intact — verified by the unchanged existing suites (SC-003, SC-005). |
| VII | Idempotency for Mutations | New `Create*` RPCs enforce `Idempotency-Key`; non-Create mutations use `resource_version` OCC. | **N/A** — no new RPC and no mutation surface. Existing capability RPCs keep their current enforcement untouched. |

**Gate result: PASS.** No violations; the Complexity Tracking table is
therefore omitted per the template instruction.

## Project Structure

### Documentation (this feature)

```text
specs/003-capability-module-extraction/
├── plan.md              # This file
├── research.md          # Phase 0 — 8 decisions, incl. the pgx parameterisation
├── data-model.md        # Phase 1 — entities and their invariants
├── quickstart.md        # Phase 1 — third-party adoption walkthrough
├── contracts/
│   └── module-api.md    # Phase 1 — the frozen public surface
├── checklists/
│   └── requirements.md  # Spec quality validation
└── tasks.md             # Phase 2 — NOT created by /speckit-plan
```

### Source Code (repository root)

```text
capability/                          # NEW — the extracted module
├── go.mod                           # module .../paladin/capability
├── go.sum
├── README.md                        # standalone docs (FR-018)
├── types.go                         # Capability, Principal, Caveats, sentinels
├── signer.go  verifier.go           # Ed25519 sign/verify
├── issuer.go  delegate.go           # issue + narrowing delegation
├── cache.go                         # revocation cache
├── jwks.go    keyloader.go          # key publication + loading
├── store.go                         # Store contract
├── usage.go                         # UsageStore[TX] contract  ← parameterised
├── metering_store.go                # MeteringStore[TX]        ← parameterised
├── metrics.go                       # OTel instrumentation (no-op by default)
├── doc.go                           # NEW — package docs (FR-018)
├── golden_test.go                   # NEW — wire-format guard, ships with the move
├── testdata/
│   └── golden_token.jwt             # frozen wire-format fixture (R-007)
├── memstore/                        # NEW — in-memory reference impl (R-006)
│   ├── memstore.go                  #   incl. staging-commit semantic for Charge
│   ├── memstore_test.go             # NEW — revocation, cascade, limit rejections
│   └── atomicity_test.go            # NEW — induced-failure rollback (SC-008)
└── example/                         # NEW — runnable walkthrough (FR-019)
    └── main.go

backend/
├── go.mod                           # gains require + replace → ../capability
└── internal/
    ├── capability/
    │   ├── alias.go                 # NEW — type UsageStore = capability.UsageStore[pgx.Tx]
    │   └── postgres/                # UNCHANGED — stays in PALADIN (856 LOC)
    │       ├── store.go
    │       └── usage.go
    ├── auth/capability_interceptor.go        # import path only
    ├── api/admin/v1/{capabilityh,billingh}/  # import path only
    ├── api/connectshim/admin/tenant_budget_server.go
    ├── api/v1/{batch,multipart,object,object_tag,presign}/
    ├── app/build_capability{,_jwks}.go, build_listeners_{admin,api}.go
    ├── worker/{capability_purger,metrics}.go
    └── config/types.go
```

**Structure Decision**: root-level nested module (`capability/`), not
`backend/pkg/...` — rationale and the accepted import-path branding cost are
recorded in [R-003](./research.md). PALADIN consumes it via `require` + `replace`
([R-004](./research.md)); a standalone CI job builds the module with no PALADIN
checkout, which is what actually enforces the no-driver guarantee.

## Boundary Summary

The question this plan answers is *where the line falls*. It falls here:

| Concern | Module | PALADIN |
|---|:---:|:---:|
| Token format, sign, verify | ✅ | |
| Issue, delegate, narrowing rules | ✅ | |
| Caveat semantics, budget ceilings, unit codes | ✅ | |
| Revocation cache + generation fencing | ✅ | |
| Key publication (JWKS), key loading | ✅ | |
| `Store` / `UsageStore[TX]` / `KeyResolver` **contracts** | ✅ | |
| In-memory reference store | ✅ | |
| OTel instrumentation (no-op default) | ✅ | |
| **Relational implementation** of the contracts | | ✅ |
| Transaction handling, outbox fan-out, ledger SQL | | ✅ |
| Cedar policy, JWT audience pinning, interceptor chain | | ✅ |
| Admin RPC surface (issue/revoke/list/usage) | | ✅ |
| Config schema, worker wiring, Helm/deploy | | ✅ |

The line is: **the module defines the primitive and its contracts; PALADIN
supplies persistence, transport, and policy.** Anything that names a database,
an RPC framework, or a policy engine stays in PALADIN.

## Phase 0 — Research

Complete. See [research.md](./research.md). Eight decisions recorded; all
Technical Context unknowns resolved. Load-bearing outcomes:

- **R-002** — generic `TX` parameter removes the only driver dependency while
  preserving compile-time safety on the charge path and keeping 28 consumers
  source-compatible behind an alias.
- **R-004** — `replace` for local development, **plus a standalone CI job**,
  because `replace` would otherwise mask a module that cannot build alone.
- **R-006** — the extraction adds **three** guards, not just relocated tests:
  wire format, charge atomicity, and generation fencing. Each covers a
  property nothing currently asserts; the atomicity one closes a gap that
  predates this feature.
- **R-007** — golden-token fixture freezes the wire format — one of three
  properties this refactor could break invisibly, and the only one whose
  failure invalidates credentials already held by running agents.
- **R-008** — five-step sequence keeping `develop` green at every commit.

## Phase 1 — Design & Contracts

Complete. Artifacts:

- **[data-model.md](./data-model.md)** — the entities crossing the boundary,
  their invariants, and which side owns each.
- **[contracts/module-api.md](./contracts/module-api.md)** — the exact public
  surface the module must publish, including the three contracts a consumer
  implements and the nine sentinel errors that must stay individually
  matchable.
- **[quickstart.md](./quickstart.md)** — the third-party adoption path,
  written without reference to object storage (FR-018 acceptance).

### Post-Design Re-check

Re-evaluating the constitution against the completed design:

- **I (Tests-First)** — still Pass. Design added one test obligation (the
  golden-token fixture, R-007); it ships in step 4 with the code it guards.
- **II (Single-Scope)** — still Pass. The five-step sequence survived design
  unchanged; no step acquired a second scope.
- **III (BACKLOG)** — still Pass, and design *added* a deferral worth
  recording: the module's `example/` is explicitly not a supported product
  surface (spec Assumptions), which is a scope boundary, not a gap.
- **IV (Pre-1.0)** — still Pass, and reinforced: no migration, no proto
  change, and the wire format is now pinned executably.
- **V / VII** — still N/A; the design introduced no deployment or mutation
  surface.
- **VI (Security Floor)** — still Pass on preservation, and strengthened:
  `contracts/module-api.md` enumerates all nine sentinels as a frozen surface,
  making FR-007 checkable by inspection rather than by hope.

**Result: PASS, no new violations.** No Complexity Tracking entries required.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Generic parameter ripples beyond the two declarations | Medium | Medium | Step 1 lands the parameterisation **alone**, before any file moves, so a bisect isolates it. The alias is the containment boundary; if ripple exceeds it, stop and reassess before step 3. |
| `replace` hides a module that cannot build standalone | **High** if unmitigated | High — silently defeats the feature's purpose | Standalone CI job (R-004) is a **required** deliverable of step 2, not step 5. |
| Wire format changes invisibly during the move | Low | **Critical** — invalidates live agent credentials | Golden-token fixture (R-007) fails CI on any format drift. The fixture is minted from pre-extraction code (step 1 of the sequence) and its **test ships inside the move commit itself**, so no commit exists where the format is unguarded. |
| Charge atomicity is unverified — it has no test today, before or after | **High** if unmitigated | **Critical** — a silent partial write corrupts spend counters | `memstore` gains a staging-commit semantic and an induced-failure test asserts both counters stay unmutated (SC-008/FR-011). Added after analysis finding G1; the property was previously moved across the boundary with no coverage on either side. |
| Import-path churn across 28 files hides a stray edit | Medium | Low | The rewrite is mechanical; step 3 is import-only, so any non-import diff in that commit is a review flag. |
| Moved code's test coverage is weaker than believed | Medium | Medium | Record the module's `go test -cover` baseline **before** step 3, so post-move coverage is compared against a number rather than a memory. |

## Counting note

The "28 consumer files" figure (16 production, 12 test) is counted **on this
branch**, which is based on `develop`. An earlier draft said 33; that number
was taken while `fix/s3adapter-part-chunking` was checked out, where commit
`6a78c15` adds two further test files under
`backend/internal/api/admin/v1/capabilityh/`. Both numbers were correct for
their branch, which is exactly why the branch is now stated alongside the
figure.

Reproduce with:

```bash
cd backend && grep -rln "internal/capability" --include="*.go" . \
  | sed 's|^\./||' | grep -vE '^internal/capability/' | wc -l
```

If that count differs when the work starts, the task enumeration in
`tasks.md` (T027–T032, the import-rewrite range) is what must be reconciled
— not this prose.

## Not Doing (and why)

- **Repository split** — deferred until there is a real external adopter;
  paying repo-split overhead for a hypothetical audience is premature
  ([R-003](./research.md)).
- **Moving the relational store into the module** — it is the reference
  implementation, not part of the contract (FR-015).
- **Any capability feature work** — externally-managed signing keys, new
  caveat types, and format changes are all out of scope (spec Out of Scope).
- **Publishing / announcing** — the extraction makes the module *importable*;
  distribution is a separate decision with its own trade-offs.

## Next Step

`/speckit-tasks` — decompose the five-step sequence from
[R-008](./research.md) into ordered, individually-verifiable tasks with the
per-commit scopes required by Principle II.
