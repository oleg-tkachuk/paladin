# capability

Budgeted, delegable, individually revocable authority for agentic workloads.

```go
import "github.com/oleg-tkachuk/paladin/capability"
```

You are not taking on a database driver or a storage SDK. This module ships
contracts; you supply storage. A bundled in-memory implementation is enough to
get started.

It is a separate Go module with its own version stream — see
[Versioning](#versioning) before you depend on it.

[docs/diagrams.md](docs/diagrams.md) draws the module: the contract boundary,
a capability's lifecycle, the verification gates, delegation, the charge and
reservation path, and how a revocation reaches the verifiers.

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

## Bind a capability to a key (DPoP)

A capability is a bearer token unless it is bound: whoever holds it can use
it, and an agent's tokens end up in prompts, tool output and logs. Binding it
to a key the agent keeps — RFC 9449 DPoP — makes a copy useless without the
key:

```go
jkt, _ := capability.KeyThumbprint(agentKey.Public()) // Ed25519 or P-256
cap, token, _ := issuer.Issue(ctx, capability.IssueRequest{ /* … */ ConfirmationJKT: jkt})

// Client: a fresh proof per request, signed over its method, URL and token.
proof, _ := capability.NewDPoPProof(agentKey, "POST", url, token, time.Now())

// Server, after Verify:
dpop := &capability.DPoPVerifier{Replay: capability.NewMemoryReplayCache(0)}
err := dpop.Check(ctx, cap, capability.DPoPRequest{Proof: proof, Method: "POST", URL: url, Token: token})
```

The token carries the thumbprint as `cnf.jkt` (RFC 7800), omitted when
unbound, so unbound tokens are byte-for-byte what they were. A delegated
child inherits its parent's binding or takes the sub-agent's own key, and
`Narrows` refuses a child that drops it. `Check` refuses a proof from another
key, for another method, URL or token, outside a minute of the verifier's
clock, or seen before; every refusal matches `ErrInvalidSignature`. The
replay cache is in-process: behind several replicas, a proof replayed to a
different one inside the window is not caught — share a `ReplayCache` to
close that.

## Narrow offline (Biscuit)

`Delegate` narrows a capability, but it needs the issuer. The Biscuit form
of the same capability (Biscuit v3) can be narrowed by whoever holds it, with
no key and no network, and a narrowing can never be undone:

```go
full, _ := issuer.Biscuit(cap) // same ID, caveats and expiry as cap

// An agent, offline, hands a worker a smaller token:
worker, _ := capability.Attenuate(full, capability.Attenuation{
    Ops:              []capability.Op{capability.OpGet},
    ResourcePrefixes: []string{"corpus/public/"},
    ExpiresAt:        time.Now().Add(10 * time.Minute),
})

// A verifier built with AcceptBiscuit: true returns the narrowed Capability.
c, err := verifier.Verify(ctx, worker, capability.AudiencePlaneData)
```

An attenuation block may restrict operations, resources, planes, the expiry,
and (on an unbound token) bind a key. The verifier folds each block in
through `Narrows`, so an attenuated token is checked exactly like a
delegated one. A block that asks for more than the token has, or holds
anything else (Datalog rules and checks included), makes the whole token
invalid. An attenuated copy spends its capability's budget and request
count, and revoking the capability revokes every copy.

A copy can also get limits of its own: `MaxRequests` and `MaxBudgetMicros` on
the `Attenuation`. Each is counted under the revocation id of the block that
set it, so siblings narrowed apart count apart, while the capability's own
limits still bound them all; a limit must fit every limit already in force.
The verifier hands them to the caller as `Capability.Copies`, and the `Meter`
counts them when they are passed on as `Copies` in `RequestBump`,
`ChargeRequest` and `ReserveRequest`. A verifier admits such a copy only with
`MeterCopies` set, which says the `Meter` behind it counts copies; without it
the token is refused rather than accepted with its limits unkept.
`verifier.BiscuitCopy` names the limits in force on a copy, on any verifier,
and a `CopyUsageReader` (`memstore` implements one) reads what each has
counted.

One copy can also be revoked on its own. `verifier.BiscuitCopy(ctx, token)`
checks the token and names its capability and the revocation id of its last
block; listing that id through `BiscuitRevocationStore.RevokeBiscuit` stops
that copy and every copy attenuated from it, and leaves the copy it came from,
its siblings and the capability's JWT working. A verifier that accepts
Biscuits needs `BiscuitRevocations` — wrap the store in a
`CachedBiscuitRevocationChecker` and clear it with the other cache.

The Biscuit seals an ordinary signed token, so a KMS-held signing key works.
That token carries the root of the Biscuit's signature chain, whose private
half is discarded once the Biscuit is built. A sealed token presented on its
own is refused, so it cannot be lifted out to shed an attenuation, and it
cannot be re-wrapped in a fresh Biscuit.

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
refund is a no-op.

Amounts are float64 in this API and exact in practice: `AmountToMicros`
rounds an amount to whole millionths and `MicrosToAmount` reverses it
without loss for anything below `MaxMicros` (fifteen significant digits,
just under a billion units). Store and sum micros, not floats.


## Reserve before a cost is known

An LLM call is priced by the tokens it ends up using. Hold an estimate first,
then settle the actual cost:

```go
r, err := usage.Reserve(ctx, capability.ReserveRequest{
    CapabilityID: cap.ID, TenantID: tenantID,
    Amount: 0.50, MaxBudget: cap.Caveats.MaxBudgetAmount, UnitCode: "USD",
    TTL: 2 * time.Minute,
})
// … the call runs …
receipt, err := usage.Settle(ctx, capability.SettleRequest{
    ReservationID: r.ID, Amount: actualCost, MaxBudget: cap.Caveats.MaxBudgetAmount,
}, nil)
```

A hold counts against every ceiling — the capability's, each ancestor's, the
tenant's — exactly as spend does, so two callers cannot both hold the last
budget. `Settle` charges the actual cost in place of the hold; above the hold,
the excess must fit, or the hold stays for you to settle lower or `Release`.
A hold never settled lapses after its TTL: run `ReleaseExpired` on a timer.

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
| `BiscuitRevocationStore` | Revoked Biscuit copies (only if you accept Biscuits) | No |
| `CopyUsageReader` | Reads Biscuit copies' own counters (only to show them) | No |
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
- **A bound token needs its key.** A key-bound capability is refused without
  a fresh DPoP proof from that key, and its children stay bound.
- **Attenuation never widens.** An offline Biscuit block that asks for more
  than its token has invalidates the token. Stripping a block or lifting out
  the sealed token is refused.
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

The Biscuit form is a second format, not a change to the first: JWTs verify
exactly as before, and a verifier takes Biscuits only with `AcceptBiscuit`.

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
