# Contract: Capability Module Public API

**Feature**: `003-capability-module-extraction` | **Date**: 2026-07-23

This is the surface the module publishes. Everything listed here is **exported
and frozen for the duration of this feature** — the extraction may relocate it
but must not change its shape. Anything not listed stays unexported.

Module path: `github.com/oleg-tkachuk/paladin/capability`

---

## 1. Contracts a consumer implements

These three interfaces are the whole extension surface (FR-004). A consumer
that implements them needs nothing else from the module.

### 1.1 `Store` — capability record persistence

```go
type Store interface {
    Insert(ctx context.Context, c Capability) error
    Get(ctx context.Context, id uuid.UUID) (*Capability, error)
    IsRevoked(ctx context.Context, id uuid.UUID) (bool, error)
    Revoke(ctx context.Context, args RevokeArgs) error
    PurgeExpired(ctx context.Context, olderThan time.Duration) (int64, error)
    ListByPrincipal(ctx context.Context, args ListByPrincipalArgs) ([]Capability, string, error)
}
```

Semantics the implementation must honour:

| Method | Required behaviour |
|---|---|
| `Get` | Returns `ErrNotFound` when absent. Callers treat absence as **forgery**, not as a missing entity. |
| `IsRevoked` | On the hot path — every verification calls it. Must be cheap. |
| `Revoke` | **Idempotent.** `RevokeArgs.CascadeChildren=true` revokes the whole descendant subtree; `false` revokes only the named id. |
| `PurgeExpired` | Housekeeping only; must not affect verification results. |
| `ListByPrincipal` | Returns `(page, nextCursor, error)`. An empty cursor means the last page. |

**No transactional requirement.** An in-memory map is a valid implementation.

### 1.2 `UsageStore[TX]` — budget and request accounting

The **only** generic contract, and the only one that touches money.

```go
type UsageStore[TX any] interface {
    BumpRequest(ctx context.Context, capID uuid.UUID, maxRequests int64) (newCount int64, err error)

    Charge(
        ctx context.Context,
        capID uuid.UUID,
        amount, maxBudget float64,
        unitCode string,
        tenantID uuid.UUID,
        op string,
        actor string,
        onCharged func(ctx context.Context, tx TX) error,
    ) (newSpent float64, err error)

    RefundCapability(ctx context.Context, capID uuid.UUID, amount float64) error
    RefundTenant(ctx context.Context, tenantID uuid.UUID, amount float64) error
    Delete(ctx context.Context, capID uuid.UUID) error
    Get(ctx context.Context, capID uuid.UUID) (Usage, error)
    GetTenantBudget(ctx context.Context, tenantID uuid.UUID) (TenantBudget, error)
    SetTenantBudget(ctx context.Context, args SetTenantBudgetArgs) (TenantBudget, error)
    ListTenantBudgets(ctx context.Context, args ListTenantBudgetsArgs) ([]TenantBudget, string, error)
}
```

**`TX` is the consumer's transaction handle type.** The module never
constructs, inspects, or constrains it — it only threads it back to the
caller's callback. This is what keeps the module free of a database driver
([R-002](../research.md)).

Ordering and atomicity the implementation must honour:

1. Check the **capability** ceiling (`maxBudget`).
2. Check the **tenant aggregate** ceiling.
3. Write the ledger record.
4. Run `onCharged` — the consumer's side effect, on the same `TX`.
5. Commit.

Required guarantees:

- If **either** ceiling rejects, return the matching sentinel
  (`ErrBudgetExceeded` / `ErrTenantBudgetExceeded`) and leave **both**
  counters unmutated. A retry after rejection must be safe.
- If `onCharged` errors, the whole charge rolls back.
- `onCharged == nil` is valid and means "no side effect" — this is the mode a
  consumer without transactions uses.
- `BumpRequest` returns `ErrRequestLimitExceeded` **without mutating** when
  `maxRequests > 0` and the increment would exceed it.

**Consumers without transactions**: instantiate `UsageStore[struct{}]` and
always pass `nil` for `onCharged`. The atomicity guarantee then covers only
what the store itself writes — documented as store-dependent (spec edge case).

### 1.3 `KeyResolver` — verification key lookup

```go
type KeyResolver interface {
    PublicKey(ctx context.Context, kid string) (ed25519.PublicKey, error)
}
```

Called per verification. Implementations are expected to cache; the module
does not cache on the consumer's behalf.

Shipped implementation:

```go
type StaticKeyResolver struct { /* ... */ }
func NewStaticKeyResolver(keys map[string]ed25519.PublicKey) *StaticKeyResolver
func (r *StaticKeyResolver) PublicKey(ctx context.Context, kid string) (ed25519.PublicKey, error)
func (r *StaticKeyResolver) SetKey(kid string, key ed25519.PublicKey)
```

`SetKey` supports rotation: publish the new key before switching signing, and
withdraw the old one only after the longest outstanding TTL has elapsed.

---

## 2. Issuance

```go
type Issuer struct { /* ... */ }
type IssuerConfig struct { /* ... */ }

func NewIssuer(cfg IssuerConfig) (*Issuer, error)

type IssueRequest struct {
    Subject   Principal
    Audience  []string
    Caveats   Caveats
    TTL       time.Duration
    NotBefore time.Time
}
func (i *Issuer) Issue(ctx context.Context, req IssueRequest) (*Capability, string, error)

type DelegateRequest struct { /* parent + narrowing */ }
func (i *Issuer) Delegate(ctx context.Context, req DelegateRequest) (*Capability, string, error)

func GenerateEd25519Keypair() (kid string, pub ed25519.PublicKey, priv ed25519.PrivateKey, err error)
```

Both `Issue` and `Delegate` return `(record, compactToken, error)`.

**Delegation invariant (FR-008)**: a child must be **narrower than or equal
to** its parent on *every* dimension — operations, resource prefixes/URIs,
request ceiling, budget ceiling, and expiry. Any widening returns
`ErrDelegationTooWide`. A child declaring a different `UnitCode` than its
parent returns `ErrUnitCodeMismatch` — the module does **not** convert between
units.

---

## 3. Verification

```go
type VerifierConfig struct { /* ... */ }
type StandardVerifier struct { /* ... */ }

func NewStandardVerifier(cfg VerifierConfig) (*StandardVerifier, error)
func (v *StandardVerifier) Verify(ctx context.Context, token, audience string) (*Capability, error)
```

Verification is **local** — signature checked against the resolved key, no
round trip to the issuer. Revocation is consulted through the configured
store/cache.

---

## 4. Sentinel errors — all nine must stay individually matchable (FR-007)

```go
var (
    ErrInvalidSignature  = errors.New("capability: invalid signature")
    ErrExpired           = errors.New("capability: expired")
    ErrNotYetValid       = errors.New("capability: not yet valid")
    ErrRevoked           = errors.New("capability: revoked")
    ErrCaveatViolation   = errors.New("capability: caveat violation")
    ErrAudienceMismatch  = errors.New("capability: audience mismatch")
    ErrBudgetExceeded    = errors.New("capability: budget exceeded")
    ErrDelegationTooWide = errors.New("capability: child wider than parent")
    ErrUnitCodeMismatch  = errors.New("capability: unit_code mismatch between parent and child")
)
```

Plus five **store-side** sentinels, which are equally exported and equally
frozen — a consumer implementing `Store` / `UsageStore[TX]` must return these
exact values, because the module's own control flow matches on them:

```go
var (
    ErrNotFound             = errors.New("capability: not found")
    ErrUsageNotFound        = errors.New("capability: usage not found")
    ErrTenantBudgetNotFound = errors.New("capability: tenant budget not found")
    ErrTenantBudgetExceeded = errors.New("capability: tenant budget exceeded")
    ErrRequestLimitExceeded = errors.New("capability: request limit exceeded")
)
```

**Fourteen sentinels total.** FR-007 names nine because those are the nine a
*verification* can reject with; the other five are the contract between the
module and a store implementation. Both sets are frozen; only the nine are
required to be distinguishable by an end consumer mapping to transport codes.

**Contract**: consumers match with `errors.Is`. Every rejection path must wrap
(never replace) its sentinel, so a consumer can map each to its own transport
status. PALADIN maps budget sentinels to a resource-exhausted status and the rest
to unauthenticated/permission-denied — that mapping stays in PALADIN.

---

## 5. Unit codes

```go
const DefaultUnitCode = "USD"
var AllowedUnitCodes = []string{"USD", "EUR", "UAH", "GBP", "UNIT"}

func IsAllowedUnitCode(u string) bool
func NormaliseUnitCode(u string) (string, error)
```

`NormaliseUnitCode("")` returns the default — this is what keeps records
written before unit tracking working. `IsAllowedUnitCode("")` is **false**;
callers wanting empty-means-default must normalise first. `UNIT` is the
abstract sentinel for non-currency metering (tokens, credits, calls).

---

## 6. Instrumentation

The module emits OpenTelemetry metrics through the **global** provider. A
consumer that configures no provider gets OTel's no-op and pays nothing
([R-005](../research.md)). No collector is required for correct operation.

---

## 7. What the module deliberately does NOT publish

| Not published | Rationale |
|---|---|
| Any relational/SQL implementation | Reference implementation, stays in PALADIN (FR-015) |
| Transaction management | The consumer owns it; the module only threads `TX` |
| Policy evaluation (Cedar or otherwise) | Authorisation *policy* is a separate concern from *authority* |
| Transport (RPC, HTTP handlers) | The primitive is transport-agnostic |
| Configuration schema / file loading | Consumers configure via their own mechanism; `IssuerConfig` / `VerifierConfig` are plain structs |

---

## 8. Compatibility obligations

| Obligation | Enforced by |
|---|---|
| Token wire format unchanged (FR-006) | Golden-token fixture in `testdata/` ([R-007](../research.md)) |
| **Charge atomicity — an `onCharged` failure leaves both counters unmutated (FR-011)** | **Induced-failure rollback test against `memstore`'s staging-commit semantic (SC-008)** |
| Two-ceiling rule — a rejection by either ceiling mutates neither counter (FR-013) | `memstore` conformance tests |
| Generation fencing — a write decided pre-revocation must not land after (FR-009) | Fencing test in the module's own suite |
| All nine sentinels distinguishable (FR-007) | This document + PALADIN's unchanged suites (SC-005) |
| No database driver in the dependency graph (FR-003) | Standalone CI job ([R-004](../research.md)) |
| No object-storage dependency (FR-002) | Same standalone job (SC-002) |
| Contracts satisfiable in memory (FR-005) | `memstore` package, used by the module's own tests |

The atomicity row is listed second deliberately: it is the property the whole
`TX` parameterisation exists to preserve, and the one with **no test anywhere
in the repository before this feature**. §1.2 states the requirement; this row
names what enforces it.
