# Quickstart: Budgeted, Delegable Agent Authority

**Feature**: `003-capability-module-extraction` | **Date**: 2026-07-23

This is the acceptance target for FR-018/FR-019 and SC-007: a developer must
reach a working issue → verify cycle using only this document. It mentions no
object storage, no database, and no Paladin concept — deliberately. If a reader
needs Paladin's docs to get through it, the extraction has not met its goal.

---

## The problem this solves

You run agents. An orchestrator spawns sub-agents, each of which calls tools
that cost money. You need to answer, per call:

- **May it?** — is this operation, on this resource, permitted?
- **Can it afford it?** — has this agent's budget run out?
- **Whose was it?** — which run, spawned by which parent, spent this?
- **Can I stop it right now?** — an agent is misbehaving; revoke it mid-flight.

Long-lived API keys answer none of these. Role-based access answers only the
first, and coarsely. A **capability** answers all four: a short-lived signed
token carrying its own operation set, resource scope, spend ceiling, and
lineage — delegable to sub-agents in strictly narrower form, and individually
revocable.

---

## Install

```bash
go get github.com/oleg-tkachuk/paladin-private/capability
```

You are **not** taking on a database driver or a storage SDK. The module ships
contracts; you supply storage. For a first run, the bundled in-memory
implementation is enough.

---

## 1. Mint a signing key

```go
kid, pub, priv, err := capability.GenerateEd25519Keypair()
```

`kid` identifies the key so tokens signed under it stay verifiable across
rotation. Keep `priv` at the issuer; publish `pub` to verifiers.

---

## 2. Wire an issuer and a verifier

```go
store := memstore.New()                       // your Store implementation
keys  := capability.NewStaticKeyResolver(map[string]ed25519.PublicKey{kid: pub})

issuer, err := capability.NewIssuer(capability.IssuerConfig{ /* key, store */ })
verifier, err := capability.NewStandardVerifier(capability.VerifierConfig{ /* keys, store */ })
```

Verification is **local** — the verifier checks the signature against the
resolved key. No round trip to the issuer on the hot path.

---

## 3. Issue a capability to an agent

```go
cap, token, err := issuer.Issue(ctx, capability.IssueRequest{
    Subject: capability.Principal{
        Type:     capability.PrincipalAgent,
        TenantID: tenantID,
        Subject:  "research-orchestrator",
        Agent: &capability.AgentPrincipal{
            AgentType: "claude-code",
            Model:     "opus",
            RunID:     runID,          // makes every later charge attributable
        },
    },
    Audience: []string{"my-service"},
    TTL:      15 * time.Minute,
    Caveats: capability.Caveats{
        Ops:              []capability.Op{"read", "search"},
        ResourcePrefixes: []string{"corpus/public/"},
        MaxRequests:      500,
        MaxBudgetAmount:  25.00,
        UnitCode:         "USD",
    },
})
```

`token` is what the agent carries. `cap` is the record you keep for audit.

Note what is *not* here: no role, no long-lived secret, no policy file. The
authority is fully described by the token itself.

---

## 4. Verify on every call

```go
cap, err := verifier.Verify(ctx, token, "my-service")
switch {
case errors.Is(err, capability.ErrExpired):          // TTL elapsed
case errors.Is(err, capability.ErrRevoked):          // pulled mid-flight
case errors.Is(err, capability.ErrBudgetExceeded):   // out of money
case errors.Is(err, capability.ErrAudienceMismatch): // token for another service
case errors.Is(err, capability.ErrInvalidSignature): // forged
}
```

Every rejection is a distinct, matchable sentinel — map each to whatever
status your transport uses. The module does not decide your HTTP or RPC codes.

---

## 5. Delegate to a sub-agent

The orchestrator narrows its own authority and hands the result to a worker
it spawns — without calling back to an admin API:

```go
child, childToken, err := issuer.Delegate(ctx, capability.DelegateRequest{
    ParentID: cap.ID,
    TTL:      2 * time.Minute,
    Caveats: capability.Caveats{
        Ops:              []capability.Op{"read"},        // dropped "search"
        ResourcePrefixes: []string{"corpus/public/2026/"}, // narrowed
        MaxBudgetAmount:  2.00,                            // 25.00 → 2.00
        UnitCode:         "USD",                           // MUST match parent
    },
})
```

**The rule**: a child may only be narrower. Ask for an operation the parent
lacks, a wider resource scope, a bigger budget, or a longer life, and you get
`ErrDelegationTooWide`. Ask for a different `UnitCode` and you get
`ErrUnitCodeMismatch` — the module does not convert currencies.

This is the property that makes the model safe to hand to an agent: an agent
cannot escalate itself, only attenuate.

---

## 6. Charge against the budget

```go
spent, err := usage.Charge(ctx, cap.ID, 0.35, cap.Caveats.MaxBudgetAmount,
    "USD", tenantID, "search", "research-orchestrator", nil)
if errors.Is(err, capability.ErrBudgetExceeded) {
    // rejected at the auth boundary — before your business logic ran
}
```

Two ceilings are checked: the capability's own, then the tenant aggregate. If
**either** rejects, neither counter moves — a retry after rejection is safe.

The trailing `nil` is the optional in-transaction callback. If your store has
transactions, pass a function and the module threads your transaction handle
back so your side effects commit atomically with the spend:

```go
usage.Charge(ctx, /* … */, func(ctx context.Context, tx MyTx) error {
    return myOutbox.Enqueue(ctx, tx, chargeEvent)   // commits with the charge
})
```

The module never inspects `MyTx` — it only hands it back. That is why this
library has no database dependency.

---

## 7. Revoke

```go
err := store.Revoke(ctx, capability.RevokeArgs{
    ID:              cap.ID,
    Reason:          "agent looping",
    Actor:           "operator@example.com",
    CascadeChildren: true,   // takes every sub-agent with it
})
```

Idempotent. `CascadeChildren` is what you want when an orchestrator has
already spawned workers: revoking the parent alone would leave them running.

---

## Bringing your own storage

Implement three interfaces and you are done:

| Interface | What it stores | Transactions needed? |
|---|---|---|
| `Store` | Capability records + revocations | No |
| `UsageStore[TX]` | Request and spend counters | Only if you want atomic side effects |
| `KeyResolver` | Public verification keys | No |

`UsageStore` is generic in `TX` — **your** transaction type. Have none?
Instantiate `UsageStore[struct{}]` and always pass `nil` for the callback.
Everything else works identically; you simply forgo the atomic-side-effect
guarantee.

See `memstore/` for a complete implementation in a few hundred lines, and
`example/` for this walkthrough as a runnable program.

---

## Key rotation

1. Publish the new public key alongside the old (`SetKey`).
2. Switch the issuer to sign with the new key.
3. Withdraw the old key only after the **longest outstanding TTL** has
   elapsed.

Skipping step 3's wait invalidates live tokens. Since TTLs here are minutes,
the wait is short — that is part of why short TTLs are the default posture.

---

## Where to go next

- `contracts/module-api.md` — the complete public surface.
- `data-model.md` — entity invariants, including the full delegation rules.
- Paladin itself — a production reference consumer with relational storage,
  policy evaluation, and an admin API on top of this primitive.
