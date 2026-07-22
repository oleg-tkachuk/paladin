# Data Model: Capability Module Extraction

**Feature**: `003-capability-module-extraction` | **Date**: 2026-07-23

This feature moves no data and changes no schema. What follows is the model as
it crosses the new boundary: which entities the **module** defines, which the
**consumer** persists, and the invariants that must survive relocation.

**Ownership rule**: the module owns every entity's *shape and rules*; the
consumer owns its *storage*. No entity is jointly owned.

---

## Capability

The bearer authority record. Defined by the module, persisted by the consumer
via `Store`.

| Field group | Purpose | Invariants that must survive |
|---|---|---|
| Identity (`ID`) | Correlates audit, usage counters, and revocation | Immutable once issued. Every charge and every audit record keys on it. |
| `Subject` (Principal) | Who the authority is for | Immutable. Carries the tenant the capability is scoped to. |
| `Audience` | Which service(s) may accept it | Verification rejects with `ErrAudienceMismatch` on any other audience. |
| Validity (`NotBefore`, `ExpiresAt`) | Lifetime window | `ErrNotYetValid` before, `ErrExpired` after. Checked on **every** verification, not just at issuance. |
| `Caveats` | The narrowing constraints | See Caveats below. |
| Lineage (`ParentID`) | Delegation chain | A child's constraints must be ≤ its parent's on every dimension. |
| `Generation` | Fences decisions made against a stale view | A write authorised under an older generation must not land after revocation. |

**Lifecycle** (no state column — state is derived):

```
issued ──(TTL elapses)──────────→ expired      (verification rejects)
   │
   ├──(delegate)────→ child capability (strictly narrower)
   │
   └──(revoke)─────→ revoked                   (verification rejects)
                        └─(cascade)→ every descendant revoked
```

Expiry and revocation are both terminal and both evaluated at verification
time. There is no "unrevoke".

---

## Principal

Who the capability is for. Three variants; only the agent variant carries
extra structure.

| Variant | Extra fields | Why it matters |
|---|---|---|
| User | — | A human acting directly. |
| Service | — | A long-lived non-human caller. |
| **Agent** | `AgentType`, `AgentVersion`, `Model`, `MCPClient`, `RunID`, `ParentAgentID` | This lineage is what makes a delegated agent call **attributable** — it answers "which run, spawned by which agent, spent this budget". |

**Invariants**:

- Agent metadata is populated **only** for the agent variant. Agent fields
  supplied alongside a non-agent kind are ignored, not smuggled through.
- `RunID` / `ParentAgentID` are optional; absent means zero, which is
  distinct from malformed. A *present but malformed* lineage id is an error —
  silently zeroing it would detach the capability from its run.
- An unrecognised kind maps to the empty type so downstream validation
  rejects it, rather than defaulting to a real principal kind.

---

## Caveats

The narrowing constraints. This is the security-critical entity: every field
here can only ever be tightened by delegation.

| Field | Meaning | Delegation rule |
|---|---|---|
| `Ops` | Permitted operations | Child's set ⊆ parent's set |
| `ResourcePrefixes`, `ResourceURIs` | Permitted resources | Child's scope ⊆ parent's scope |
| `MaxRequests` | Request ceiling | Child ≤ parent |
| `MaxBudgetAmount` | Spend ceiling | Child ≤ parent |
| `UnitCode` | Unit the budget is denominated in | Child **must equal** parent — no conversion (`ErrUnitCodeMismatch`) |
| `AllowTaintedRead` | Permits reading data flagged as tainted | Child may not enable what parent disabled |
| `IdempotencyKeyRequired` | Forces idempotency on mutations | Child may not relax it |
| `SourceIPCIDR` | Network origin restriction | Child's set ⊆ parent's set |

**The invariant, stated once**: *no delegation may widen any dimension.*
Violations return `ErrDelegationTooWide`. This is the property the whole
delegation model rests on, and the one most damaged by a careless refactor.

---

## Usage

Running counters for a single capability. Defined by the module, persisted by
the consumer via `UsageStore[TX]`.

| Field | Meaning |
|---|---|
| `CapabilityID` | Which capability |
| `RequestCount` | Requests consumed against `MaxRequests` |
| `SpentAmount` | Budget consumed against `MaxBudgetAmount` |
| `UnitCode` | Unit of `SpentAmount` |

**Invariants**:

- Absence means *never used*, not an error state — surfaced as
  `ErrUsageNotFound` so a caller can distinguish it from a failure.
- An empty `UnitCode` on a record predating unit tracking resolves to
  `DefaultUnitCode`. A response must never carry a bare number with no unit.
- Counters only advance on a **committed** charge. A rejected charge leaves
  them untouched.

---

## Tenant budget

An aggregate ceiling above individual capabilities, so a set of tokens cannot
collectively exceed their owner's limit.

| Field | Meaning |
|---|---|
| `TenantID` | Whose ceiling |
| `MaxBudget` | Aggregate ceiling |
| `Spent` | Aggregate consumed |
| `UnitCode` | Unit for both |

**Invariant — the two-ceiling rule**: a charge is checked against the
capability ceiling *then* the tenant ceiling. If **either** rejects, neither
counter is mutated. There is no partial application, and therefore no refund
needed on the rejection path.

---

## Revocation entry

Withdraws a capability, optionally cascading to descendants.

| Field | Meaning |
|---|---|
| `ID` | Which capability |
| `Reason` | Why — operator-facing |
| `Actor` | Who revoked it — the audit trail |
| `CascadeChildren` | Whether the whole descendant subtree goes with it |

**Invariants**:

- **Idempotent** — revoking twice is a no-op, not an error.
- Propagates within the consumer's configured cache window.
- Fences in-flight writes via `Generation`: a write decided against a
  pre-revocation view must not land afterwards.

---

## Signing key set

The published verification keys, allowing verification without an issuer round
trip.

**Invariants**:

- A token signed under a **retired but still published** key must still
  verify — this is what makes rotation non-disruptive.
- Once a key is withdrawn, tokens signed under it stop verifying. The
  withdrawal deadline is therefore max outstanding TTL, not "immediately".
- Private key material never leaves the issuer; only public keys are
  published.

---

## Boundary summary

| Entity | Shape & rules | Storage |
|---|---|---|
| Capability | Module | Consumer (`Store`) |
| Principal | Module | Embedded in Capability |
| Caveats | Module | Embedded in Capability |
| Usage | Module | Consumer (`UsageStore[TX]`) |
| Tenant budget | Module | Consumer (`UsageStore[TX]`) |
| Revocation entry | Module | Consumer (`Store`) |
| Signing key set | Module | Consumer (`KeyResolver`) |

No entity requires relational storage. Every one is representable in memory,
which is what `memstore` demonstrates and what SC-006 requires.
