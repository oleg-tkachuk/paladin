# capability

Budgeted, delegable, individually revocable authority for agentic workloads.

```go
import "github.com/oleg-tkachuk/paladin/capability"
```

You are not taking on a database driver or a storage SDK. This module ships
contracts; you supply storage. A bundled in-memory implementation is enough to
get started.

It is a separate Go module, but an **in-tree** one, and it does not publish
versions you can `go get` — see [Versioning](#versioning) before you depend
on it from outside this repository.

## The problem

You run agents. An orchestrator spawns sub-agents, each calling tools that
cost money. Per call you need to answer:

- **May it?** — is this operation, on this resource, permitted?
- **Can it afford it?** — has this agent's budget run out?
- **Whose was it?** — which run, spawned by which parent, spent this?
- **Can I stop it now?** — an agent is misbehaving; revoke it mid-flight.

Long-lived API keys answer none of these. Role-based access answers only the
first, and coarsely. A **capability** answers all four: a short-lived signed
token carrying its own operation set, resource scope, spend ceiling and
lineage — delegable to sub-agents in strictly narrower form, and individually
revocable.

## Five minutes

```go
kid, pub, priv, _ := capability.GenerateEd25519Keypair()

records := memstore.New[struct{}]()          // your Store
usage   := memstore.NewUsage[struct{}](records)

signer, _ := capability.NewEd25519Signer(kid, priv)
issuer, _ := capability.NewIssuer(capability.IssuerConfig{
    Signer: signer, Store: records, IssuerName: "my-issuer",
})
verifier, _ := capability.NewStandardVerifier(capability.VerifierConfig{
    Keys:           capability.NewStaticKeyResolver(map[string]ed25519.PublicKey{kid: pub}),
    Revocations:    records,
    TrustedIssuers: []string{"my-issuer"},
})

cap, token, _ := issuer.Issue(ctx, capability.IssueRequest{
    Subject: capability.Principal{
        Type: capability.PrincipalAgent, TenantID: tenantID,
        Subject: "research-orchestrator",
        Agent:   &capability.AgentPrincipal{AgentType: "my-agent", RunID: runID},
    },
    Audience: []string{"my-service"},
    TTL:      15 * time.Minute,
    Caveats: capability.Caveats{
        Ops:              []capability.Op{capability.OpGet, capability.OpShare},
        ResourcePrefixes: []string{"corpus/public/"},
        MaxRequests:      500,
        MaxBudgetAmount:  25.00,
        UnitCode:         "USD",
    },
})
```

`token` is what the agent carries; `cap` is the record you keep for audit.
Note what is absent: no role, no long-lived secret, no policy file. The
authority is fully described by the token.

`go run ./example` runs this end to end.

## Verify on every call

```go
cap, err := verifier.Verify(ctx, token, "my-service")
switch {
case errors.Is(err, capability.ErrExpired):          // TTL elapsed
case errors.Is(err, capability.ErrRevoked):          // pulled mid-flight
case errors.Is(err, capability.ErrAudienceMismatch): // token for another service
case errors.Is(err, capability.ErrInvalidSignature): // forged
}
```

Verification is **local** — the signature is checked against the resolved key,
with no round trip to the issuer. Every rejection is a distinct sentinel; map
each to whatever status your transport uses. This module does not decide your
HTTP or RPC codes.

## Delegate — attenuate, never escalate

An orchestrator narrows its own authority and hands the result to a worker it
spawned, without calling back to any admin API:

```go
child, childToken, err := issuer.Delegate(ctx, capability.DelegateRequest{
    Parent:  *cap,                      // the verified parent
    Subject: capability.Principal{ /* the sub-agent */ },
    TTL:     2 * time.Minute,
    Caveats: capability.Caveats{
        Ops:              []capability.Op{capability.OpGet},  // dropped OpShare
        ResourcePrefixes: []string{"corpus/public/2026/"},     // narrowed
        MaxBudgetAmount:  2.00,                                // 25.00 → 2.00
        UnitCode:         "USD",                               // MUST match parent
    },
})
```

**The rule**: a child may only be narrower. Ask for an operation the parent
lacks, a wider scope, a bigger budget or a longer life, and you get
`ErrDelegationTooWide`. Ask for a different `UnitCode` and you get
`ErrUnitCodeMismatch` — units are never converted.

This is the property that makes the model safe to hand to an agent: it can
attenuate itself, but never escalate.

## Charge against the budget

```go
spent, err := usage.Charge(ctx, cap.ID, 0.35, cap.Caveats.MaxBudgetAmount,
    "USD", tenantID, "search", "orchestrator", nil)
if errors.Is(err, capability.ErrBudgetExceeded) {
    // rejected at the auth boundary — before your business logic ran
}
```

Two ceilings are checked: the capability's own, then the tenant aggregate. If
**either** rejects, neither counter moves, so a retry after rejection is safe.

The trailing `nil` is an optional in-transaction callback. If your store has
transactions, pass a function and the module threads **your** handle back so
your side effects commit atomically with the spend:

```go
usage.Charge(ctx, /* … */, func(ctx context.Context, tx MyTx) error {
    return myOutbox.Enqueue(ctx, tx, chargeEvent)   // commits with the charge
})
```

The module never inspects `MyTx` — it only hands it back. That is why this
library has no database dependency.

## Revoke

```go
records.Revoke(ctx, capability.RevokeArgs{
    ID: cap.ID, Reason: "agent looping", Actor: "operator@example.com",
    CascadeChildren: true,   // takes every sub-agent with it
})
```

Idempotent. Cascade is what you want once an orchestrator has spawned workers
— revoking the parent alone would leave them running.

## Bring your own storage

Implement these and you are done:

| Interface | Stores | Transactions needed? |
|---|---|---|
| `Store` | Capability records + revocations | No |
| `UsageStore[TX]` | Request and spend counters | Only for atomic side effects |
| `KeyResolver` | Public verification keys | No |

`Store` and `UsageStore` are **separate types**: both declare a method named
`Get` with different signatures, so one type cannot satisfy both. `memstore`
shows the split.

`UsageStore` is generic in `TX` — *your* transaction type. Have none?
Instantiate `UsageStore[struct{}]` and always pass `nil` for the callback.
Everything works identically; you simply forgo the atomic-side-effect
guarantee.

## Key rotation

1. Publish the new public key alongside the old (`SetKey`).
2. Switch the issuer to sign with the new key.
3. Withdraw the old key only after the **longest outstanding TTL** has elapsed.

Skipping the wait in step 3 invalidates tokens agents are still holding. Since
TTLs here are minutes, the wait is short — which is part of why short TTLs are
the default posture.

## Guarantees

- **Token format is stable.** A golden fixture in `testdata/` fails CI on any
  wire-format drift, because tokens outlive deployments.
- **Charges are atomic.** A failed side effect, or a rejection by either
  ceiling, leaves both counters *and* the ledger untouched.
- **No hidden dependencies.** CI asserts the resolved dependency graph
  contains no database driver and no storage SDK.
- **Tests need nothing.** The suite runs with no database, no network and no
  container.

## Known limitation: the `paladin_` claim namespace

Token claims are namespaced `paladin_principal`, `paladin_caveats`, `paladin_parent_id`,
`paladin_gen` — a leftover from where this originated. They are **part of the
frozen wire format**, so renaming them would invalidate every token already
issued. They stay until a deliberate format migration, which is a separate
decision with its own compatibility window rather than a cleanup.

## Versioning

Two surfaces, two different promises — they are not the same thing and it
matters which one you depend on.

**The token wire format is frozen.** Claims, signature algorithm and
serialisation do not change without a major version and a migration window.
Tokens are credentials that outlive the process that issued them: a format
change invalidates capabilities agents are still holding, which no amount of
"it's pre-1.0" makes acceptable. A golden fixture in `testdata/` fails CI on
any drift.

**The Go API is pre-1.0 and may change.** Signatures, type names and struct
fields can move between minor versions. Read the diff before bumping.

**There is no version stream to pin.** One tag exists, `capability/v0.1.0`,
and no more are cut: the release pipeline versions Paladin, and Go resolves a
subdirectory module from tags carrying that subdirectory as a prefix, so a
Paladin release publishes nothing this module can be fetched by. An outside
consumer gets a commit pseudo-version, not `@v0.1.1`.

That is a deliberate stop, not an oversight. Mirroring Paladin's tag under the
`capability/` prefix does not work — Paladin is past v1, and Go requires a
matching major-version suffix in the module path from v2 onward, so
`capability/v4.0.0` would be created and then refused at `go get`. Running a
second, independently-versioned release pipeline does work, and is the right
answer the moment someone outside this repository actually depends on the
module. Until then it is machinery maintained for nobody.

So: this is an in-tree library with an enforced boundary, not a published
package. Paladin consumes it through a `replace` directive; the lone tag is
hygiene. If you want to build on it, vendor it or pin a commit — and open an
issue, because a real consumer is exactly what would justify the pipeline.

## Status

v0.1.0 — an **internal library** of [Paladin](../README.md),
extracted so the primitive is clean, self-contained and reusable, not to ship
it as a separate product. Paladin is its reference consumer: a deployment with
relational storage, policy evaluation and an admin API on top of it. It stays
in-tree, consumed via a `replace` directive; the tag exists as hygiene, not as
a promise of external support. The Go API is pre-1.0 — read the diff before
bumping.
