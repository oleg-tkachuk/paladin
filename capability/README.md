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
    IssuedBy: capability.Principal{Subject: "operator@example.com"}, // who asked
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
with no round trip to the issuer, and nothing in the claims is read until it
has. Every rejection is a distinct sentinel; map each to whatever status your
transport uses. This module does not decide your HTTP or RPC codes.

The token must carry `typ: paladin-cap+jwt` and a tenant, and may be at most
`MaxTokenBytes` long. When several issuers share a verifier, set
`KeyIssuers` (kid → issuer) so one issuer's key cannot sign for another.
Verifiers running apart from the issuer resolve keys with
`RemoteJWKSResolver`, which caches the issuer's JWKS, picks up a rotated-in
kid on first sight (rate-limited), and fails closed after `MaxStale`.

## Enforce the caveats on every operation

Verification proves the token is genuine; it does not know what the bearer is
about to do. Ask the caveats, through the one definition the module ships:

```go
if err := cap.Caveats.CheckSource(clientAddr); err != nil { /* per connection */ }

err := cap.Caveats.Check(capability.CheckRequest{
    Op:                capability.OpGet,
    Resource:          "corpus/public/2026/report.pdf",
    HasIdempotencyKey: req.Header.Get("Idempotency-Key") != "",
    ResourceTainted:   object.Flagged, // your taint signal, if you track one
})
switch {
case errors.Is(err, capability.ErrOpNotAllowed):
case errors.Is(err, capability.ErrResourceNotAllowed):
case errors.Is(err, capability.ErrIdempotencyKeyRequired):
case errors.Is(err, capability.ErrTaintedReadNotAllowed):
}
// every one of these also matches capability.ErrCaveatViolation
```

Resource prefixes match at a `/` segment boundary: `corpus/public` covers
`corpus/public/x` but not `corpus/public-secret`. An operation that cannot name
its resource (`Resource: ""`) is refused by a resource-restricted capability —
for an operation over a set, pass the prefix that bounds the set.
`AllowTaintedRead` can only act on a taint signal you supply; if you track
none, it protects nothing, and you should say so.

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
`ErrUnitCodeMismatch` — units are never converted. Each child resource must be
reachable by the parent (a parent pinned to exact URIs admits no child
prefix), and each child network must lie inside a parent network.

To hand a child exactly the parent's caveats, say so with
`InheritCaveats: true`; caveats without any `Ops` are rejected rather than
silently replaced by the parent's. A parent that has expired, or that is
revoked (itself or any ancestor), cannot delegate.

Narrowing bounds each child. The tree as a whole is bounded at use: every
charge and request also counts against each ancestor, so a parent holding
25.00 that delegates 20.00 to each of two workers can still spend 25.00 in
total, not 40.00.

This is the property that makes the model safe to hand to an agent: it can
attenuate itself, but never escalate.

## Charge against the budget

```go
receipt, err := usage.Charge(ctx, capability.ChargeRequest{
    CapabilityID: cap.ID, TenantID: tenantID,
    Amount: 0.35, MaxBudget: cap.Caveats.MaxBudgetAmount, UnitCode: "USD",
    Op: "search", Actor: "orchestrator",
}, nil)
if errors.Is(err, capability.ErrBudgetExceeded) {
    // rejected at the auth boundary — before your business logic ran
}
```

Three ceilings are checked: the capability's own, each ancestor's, then the
tenant aggregate. If **any** rejects, no counter moves, so a retry after
rejection is safe.

`receipt.ChargeID` names the ledger row. Refund against it:

```go
usage.Refund(ctx, capability.RefundRequest{ChargeID: receipt.ChargeID, Amount: 0.10}) // partial
usage.Refund(ctx, capability.RefundRequest{ChargeID: receipt.ChargeID})               // the rest
```

A refund returns spend to every counter the charge took it from, and never
more than the charge: `Amount: 0` refunds what is left, so a retried full
refund is a no-op. Charge-then-refund is also how you settle a cost you only
learn afterwards — charge the estimate, refund the difference.

The trailing `nil` is an optional in-transaction callback. If your store has
transactions, pass a function and the module threads **your** handle back so
your side effects commit atomically with the spend:

```go
usage.Charge(ctx, req, func(ctx context.Context, tx MyTx) error {
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

Idempotent. Verifiers that cache revocation answers learn of it within the
cache TTL, or at once if you call `CachedRevocationChecker.Clear` when a
revocation is announced (Paladin listens on a Postgres channel for that).
Revoking a capability revokes everything delegated from it, with or
without the flag: `IsRevoked` answers for the whole chain. `CascadeChildren`
also writes a revocation entry per descendant, so the audit trail names each
capability that was stopped.

## Bring your own storage

Implement these and you are done:

| Interface | Stores | Transactions needed? |
|---|---|---|
| `Store` | Capability records + revocations | No |
| `UsageStore[TX]` = `Meter[TX]` + `TenantBudgets` + `UsageHousekeeping` | Request and spend counters, the charges ledger, tenant ceilings | Only for atomic side effects |
| `KeyResolver` | Public verification keys | No |

Two obligations are easy to miss. `Store.IsRevoked` answers for the
capability's whole delegation chain, and `Meter` applies every charge and
request to each ancestor as well, reading the ancestor's ceilings from its
record. Code that only meters can depend on `Meter` alone; code that only
administers tenant ceilings on `TenantBudgets` alone.

`Store` and `UsageStore` are **separate types**: both declare a method named
`Get` with different signatures, so one type cannot satisfy both. `memstore`
shows the split.

`UsageStore` is generic in `TX` — *your* transaction type. Have none?
Instantiate `UsageStore[struct{}]` and always pass `nil` for the callback.
Everything works identically; you forgo the atomic-side-effect
guarantee.

## Key rotation

1. Publish the new public key alongside the old (`SetKey`, or add it to the
   served JWKS — `MarshalJWKS` output is sorted by kid, so it is stable).
2. Switch the issuer to sign with the new key.
3. Withdraw the old key (`RemoveKey`) only after the **longest outstanding
   TTL** has elapsed.

Skipping the wait in step 3 invalidates tokens agents are still holding. Since
TTLs here are minutes, the wait is short — which is part of why short TTLs are
the default posture.

## Guarantees

- **Token format is stable.** A golden fixture in `testdata/` fails CI on any
  wire-format drift, because tokens outlive deployments.
- **Charges are atomic.** A failed side effect, or a rejection by any
  ceiling, leaves every counter *and* the ledger untouched.
- **Delegation never widens.** A fuzz test (`FuzzNarrowsNeverWidens`) checks
  that whatever `Narrows` accepts reaches no resource the parent cannot.
- **No hidden dependencies.** The resolved dependency graph contains no
  database driver and no storage SDK; `isolation_test.go` asserts it, so
  `task -t Taskfile.dev.yaml verify-capability` and CI fail if one appears.
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

**The version stream is `capability/vX.Y.Z`.** The release workflow cuts it
from the commits that touch `capability/`, independently of Paladin's own
version: `feat` a minor, `fix` a patch, and — while the module is pre-1.0 — a
breaking change a minor too. Pin `go get …/capability@vX.Y.Z`.

Paladin's own tag cannot be mirrored here: Paladin is past v1, and Go requires
a matching major-version suffix in the module path from v2 onward, so
`capability/v4.0.0` would be created and then refused at `go get`. Paladin
itself consumes the module through a `replace` directive, so its releases do
not wait on these tags.

## Status

v0.1.0 — an **internal library** of [Paladin](../README.md),
extracted so the primitive is clean, self-contained and reusable, not to ship
it as a separate product. Paladin is its reference consumer: a deployment with
relational storage, policy evaluation and an admin API on top of it. It stays
in-tree, consumed via a `replace` directive; the tag exists as hygiene, not as
a promise of external support. The Go API is pre-1.0 — read the diff before
bumping.
