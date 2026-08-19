# Phase 0 Research: Capability Module Extraction

**Feature**: `003-capability-module-extraction` | **Date**: 2026-07-23

All Technical Context unknowns are resolved below. Each decision records what
was chosen, why, and what was rejected.

---

## R-001: Baseline coupling survey (input to every other decision)

**Finding**: `internal/capability` imports **zero** Paladin-internal packages.

Verified by:

```bash
grep -rhn "oleg-tkachuk/paladin" internal/capability/*.go \
  | grep -v _test | sed -E 's|.*paladin/||; s|".*||' | sort -u
# → internal/capability   (self-reference only)
```

External dependency set of the core: stdlib (`context`, `crypto/ed25519`,
`crypto/rand`, `crypto/x509`, `encoding/*`, `errors`, `fmt`, `os`, `slices`,
`strings`, `sync`, `sync/atomic`), plus `github.com/google/uuid`,
`github.com/jackc/pgx/v5`, and `go.opentelemetry.io/otel{,/attribute,/metric}`.

**Consequence**: this is a packaging exercise, not a decoupling exercise. The
only genuine architectural work is R-002. Everything else is mechanical.

**Sizing**:

| Unit | LOC | Destination |
|---|---:|---|
| Core (`types/signer/verifier/issuer/delegate/cache/jwks/keyloader/store/usage/metering_store/metrics`) | 2 701 | **Module** |
| `internal/capability/postgres` (`store.go`, `usage.go`) | 856 | **Stays in Paladin** |
| Consumer files across Paladin | 28 files (16 prod, 12 test) | **Stays in Paladin** |

---

## R-002: Removing the `pgx` leak from the usage contract

**Problem.** The module cannot depend on a database driver (FR-003), but the
usage contract names one:

```go
// internal/capability/usage.go:78 (and metering_store.go:66)
Charge(
    ctx context.Context, capID uuid.UUID,
    amount, maxBudget float64, unitCode string,
    tenantID uuid.UUID, op, actor string,
    onCharged func(ctx context.Context, tx pgx.Tx) error,   // ← the leak
) (newSpent float64, err error)
```

`onCharged` exists for a real reason and must survive: it runs **inside** the
charge transaction, after the ledger row and before commit, so the event
outbox rows commit atomically with the spend (ADR-0003). Removing it would
reopen the dual-write window. Only one production call site constructs it —
`internal/auth/capability_interceptor.go:535`.

**Decision: parameterise the transaction handle as a generic type parameter.**

```go
// module
type UsageStore[TX any] interface {
    Charge(..., onCharged func(ctx context.Context, tx TX) error) (float64, error)
    ...
}
type MeteringStore[TX any] struct { Inner UsageStore[TX] }
```

```go
// Paladin, one line, keeps all 28 consumers source-compatible
type UsageStore = capability.UsageStore[pgx.Tx]
```

**Rationale**:

- The library never names, imports, or constrains a driver — FR-003 and
  FR-012 satisfied structurally rather than by convention.
- Type safety is preserved end-to-end. Paladin's callback keeps its exact
  `pgx.Tx` signature; the compiler still checks it.
- A consumer with no transactions instantiates `UsageStore[struct{}]` (or
  any placeholder) and passes `nil` — the contract already documents "pass
  nil to skip fan-out", so the no-transaction path is a supported mode, not
  a degradation.
- The alias absorbs the churn: consumers referencing Paladin's `UsageStore`
  compile unchanged. Only the two declaration sites gain a type parameter.
- A non-generic alias to an instantiated generic type is valid Go on every
  supported release; this does not depend on the Go 1.24 generic-alias
  feature.

**Alternatives rejected**:

| Alternative | Rejected because |
|---|---|
| `onCharged func(ctx, tx any) error` + type assertion at the call site | Trades a compile-time guarantee for a runtime panic on the **charge** path — the one path where a mistake corrupts money counters. Cheapest to write, worst place to be wrong. |
| Define a narrow `Tx` interface in the module | `pgx.Tx` would have to satisfy it, which means either an adapter at every call site or a method set the library has no business specifying. Reintroduces the coupling as a shape instead of an import. |
| Drop `onCharged`; let the caller run the fan-out after `Charge` returns | Reopens the ADR-0003 dual-write window the callback was introduced to close. Non-starter. |
| Split `Charge` into a base method plus an optional `TransactionalCharger` interface | The transactional variant is the *only* one Paladin uses; the split would leave a contract whose primary implementation is the optional half. Complexity with no consumer. |

**Cost**: two declarations gain `[TX any]`; every Paladin consumer is untouched
behind the alias.

---

## R-003: Module location and import path

**Decision**: a nested module at repository-root `capability/`, module path
`github.com/oleg-tkachuk/paladin/capability`.

**Rationale**:

- Nested modules are resolvable by `go get` today; no repository split, no
  new CI, no publishing pipeline needed for the extraction to be *usable*.
- Root-level (not `backend/pkg/...`) keeps the import path one segment deep
  and signals that the module is not a subordinate of the backend service.
- Cross-cutting changes stay atomic: a change touching both the module and
  its Paladin consumer remains one commit, one review, one CI run.

**Known downside, accepted deliberately**: the import path still contains
`paladin`, which works against the positioning that the
primitive is independent of object storage. This is a *branding* cost, not a
technical one, and it is reversible: a future move to a dedicated repository
changes `module` in `go.mod` and consumers' import lines, nothing else.
Deferring that until there is at least one external adopter avoids paying
repository-split overhead for a hypothetical audience. Recorded in
`spec.md` Out of Scope and to be added to `BACKLOG.md` per Principle III.

**Alternatives rejected**:

| Alternative | Rejected because |
|---|---|
| `backend/pkg/capability` nested module | Import path `.../paladin/backend/pkg/capability` reads as an internal of a service; four segments of noise for an external consumer. |
| Separate repository immediately | Loses atomic cross-repo changes and doubles CI for zero current adopters. Correct move *after* adoption, not before. |
| Keep in `internal/`, no module | `internal/` is unimportable by any external consumer by language rule. Fails FR-001 outright. |

---

## R-004: `replace` directive vs published version during development

**Decision**: Paladin's `backend/go.mod` uses a `replace` directive pointing at
the sibling module for local development, with a real version requirement
recorded alongside it.

**Rationale**: without `replace`, every module change would need a tag before
Paladin could consume it, which makes the extraction unworkable day to day. With
`replace`, the working tree always builds against the local source while the
`require` line documents the intended version.

**Verification requirement**: `replace` masks a broken published module, so
CI MUST additionally build the module **standalone** (its own `go build ./...`
and `go test ./...` from its own directory) to prove it is self-contained.
That standalone job is what actually enforces FR-002/FR-003 — not the
Paladin build.

---

## R-005: Observability without forcing a collector

**Decision**: keep the existing OpenTelemetry instrumentation in the module,
relying on OTel's no-op default provider.

**Rationale**: `otel` is a widely-accepted dependency, and its global default
is a no-op — a consumer that never configures a provider pays nothing and is
not required to run a collector (spec assumption "consumers supply their own
observability"). Stripping instrumentation would regress Paladin's existing
dashboards for no consumer benefit.

**Alternatives rejected**: a hand-rolled metrics interface (reinvents OTel and
forces an adapter on the one consumer that already speaks OTel); build tags
(untested build combinations, a known source of rot).

---

## R-006: Test strategy across the boundary

**Decision**:

1. Existing `*_test.go` files move **with** their source into the module and
   must pass there unchanged (`jwks_test.go`, `signer_test.go`,
   `verifier_test.go`).
2. The module gains an **in-memory reference implementation** of `Store` and
   `UsageStore[TX]` under `capability/memstore/`, used by its own tests and
   doubling as the worked example for FR-019. It carries a **staging-commit
   semantic** for `Charge` — without one, no implementation in the module is
   capable of demonstrating rollback, and SC-008 stays unverifiable.
3. Paladin's existing capability/auth/billing/outbox tests stay in Paladin and must
   pass with **no assertion changed** (FR-017, SC-003).
4. A new **standalone CI job** builds and tests the module from its own
   directory with no Paladin checkout on the module path (R-004).
5. **Guards** are added for every property this refactor could break
   silently. The authoritative list is `tasks.md` Phases 3–4; deliberately
   *not* restated as a count here, because a hardcoded number has already gone
   stale twice as the list grew. The load-bearing ones:
   - *wire format* — the golden-token fixture (R-007), shipping in the same
     commit as the move;
   - *charge atomicity* — an induced `onCharged` failure must leave both
     counters unmutated (FR-011/SC-008). This property has **no test anywhere
     in the repository today**, so the guard closes a pre-existing gap rather
     than merely preserving coverage;
   - *generation fencing* — a write authorised against a pre-revocation view
     must not land afterwards (FR-009);
   - *key rotation, missing-record-as-forgery, unit-code helpers* — three
     spec edge cases that likewise had no coverage anywhere (added after
     analysis findings E1/E2).

**Rationale**: the in-memory store is the only new test *infrastructure*
needed, and it does double duty as documentation. The module's suite must run
with no database, no network, no container (SC-006) — the in-memory store is
what makes that true. The guards are tests, not infrastructure, and each
exists because the move's blast radius reaches a property nothing else asserts.

**Constitution note (Principle I)**: this satisfies tests-first because the
extraction ships no new behaviour; the guard rail is that the *existing*
suites keep passing on both sides of the boundary. New tests are limited to
the in-memory store, the standalone-build job, and the guards above —
each landing in the same commit as the code it protects.

> **Amended 2026-07-23** (analysis findings G1/G2, then R1). The original
> decision said "new tests are limited to the in-memory store and the
> standalone-build job", which the task list later contradicted. Recorded as
> an amendment rather than a silent rewrite: the scope genuinely grew when the
> atomicity gap was discovered, and the record should show that.

---

## R-007: Wire-format immutability under a source-level refactor

**Decision**: treat the token format as frozen (FR-006) and prove it with a
**golden-token test**: a fixture token, signed under a fixture key, checked
into the module and verified byte-for-byte by the module's suite.

**Rationale**: the constitution permits breaking changes pre-1.0 (Principle
IV) but tokens are credentials that outlive a deployment — a format break
invalidates live capabilities held by running agents. A golden fixture makes
an accidental format change fail CI rather than fail in production.

**Scope note**: this is one of several guards the extraction adds rather
than relocates — alongside charge atomicity and generation fencing (R-006
item 5). Each is justified the same way: the refactor's blast radius reaches
a property that nothing currently asserts. For the wire format specifically,
the consequence of an undetected change is the worst of the three, because it
invalidates credentials already held by running agents rather than failing a
build.

---

## R-008: Sequencing to keep Paladin green at every commit

**Decision**: land in this order, each step independently building and
passing tests:

1. **Parameterise `onCharged` in place** (still under `internal/`), add the
   Paladin-side alias. Paladin compiles, all tests pass, no module exists yet.
2. **Create the module skeleton** (`go.mod`, `replace`, standalone CI job)
   with no code moved.
3. **Move core files** into the module; rewrite Paladin imports to the module
   path. Postgres implementations stay put. **The golden-token test ships in
   this step**, not the next — it guards the move, so deferring it would leave
   one commit where the wire format is unguarded (Principle I; amended after
   analysis finding C1).
4. **Add `memstore` + the remaining guards + example**; wire the standalone CI
   job to enforce them.
5. **Documentation and versioning** (module README, first tag).

**Rationale**: step 1 is the only step with semantic risk, and it is isolated
from the move so that a bisect lands on it unambiguously. Steps 3–5 are
mechanical. No step leaves `develop` broken, satisfying the single-scope
commit rule (Principle II) with one scope per step.

---

## Resolved Technical Context

| Item | Value |
|---|---|
| Language/Version | Go 1.26 (matches `backend/deploy/Dockerfile` `GO_VERSION`) |
| Module deps | `github.com/google/uuid`, `go.opentelemetry.io/otel` — **no** database driver, **no** storage SDK |
| Storage | None in the module; contracts only. Reference relational implementation stays in Paladin |
| Testing | `go test`, in-memory store, golden-token fixture, standalone module CI job |
| Target platform | Any Go-supported platform; library, no runtime assumptions |
| Project type | Library (nested module) + existing web service as reference consumer |
| Performance goals | No regression on the request authorisation path (SC-009); verification stays local (no issuer round trip) |
| Constraints | Token wire format frozen; module suite runs with no external infrastructure |
| Scale/Scope | ~2 700 LOC moved, 2 signatures changed, 28 consumer files preserved |
