# Diagrams

Drawn from the code of this module. The prose that explains the behaviour is in
[README.md](../README.md) and the package documentation in
[doc.go](../doc.go); the identifiers on the diagrams are the exported names, so
a renamed type shows up as a stale label.

Colours follow the rest of the repository: blue for callers, green for what
this module ships, amber for the contracts a consumer implements and stores
behind them, grey dashed for optional pieces, pink for things outside the
process.

## Contract boundary

The module ships the primitive and three contracts; the consumer supplies the
storage behind them. `memstore` is the in-memory reference implementation, and
Paladin's PostgreSQL adapters are another.

```mermaid
flowchart LR
    caller(["Consumer service<br/>orchestrator · tool · API"])

    subgraph mod ["capability (this module)"]
        direction TB
        issuer["<b>Issuer</b><br/>Issue · Delegate"]
        signer["<b>Signer</b><br/>NewEd25519Signer · JWT EdDSA"]
        narrows["<b>Narrows</b><br/>child ⊆ parent"]
        verifier["<b>StandardVerifier</b><br/>Verify(token, audience)"]
        caveats["<b>Caveats</b><br/>Check · CheckSource · MatchResource"]
        cache["CachedRevocationChecker<br/>TTL · single-flight"]
        metering["MeteringStore[TX]<br/>WithMetering · OTel counters"]
        static["StaticKeyResolver<br/>SetKey · RemoveKey"]
        remote["RemoteJWKSResolver<br/>ETag · MaxStale"]
    end

    subgraph contracts ["contracts the consumer implements"]
        direction TB
        store["<b>Store</b><br/>Insert · Get · IsRevoked · Revoke<br/>PurgeExpired · ListByPrincipal"]
        usage["<b>UsageStore[TX]</b><br/>Meter[TX]: Charge · Reserve · Settle · Refund<br/>TenantBudgets · UsageHousekeeping"]
        keys["<b>KeyResolver</b><br/>PublicKey(kid)"]
    end

    impl[("memstore · PostgreSQL · …")]
    jwks{{"Issuer JWKS endpoint<br/>MarshalJWKS"}}
    otel{{"OTel MeterProvider"}}

    caller --> issuer
    caller --> verifier
    caller --> caveats
    caller --> metering
    issuer --> narrows
    issuer --> signer
    issuer --> store
    verifier --> keys
    verifier --> cache --> store
    metering --> usage
    metering -. "metrics" .-> otel
    static -. "implements" .-> keys
    remote -. "implements" .-> keys
    remote -- "HTTP GET" --> jwks
    store --> impl
    usage --> impl

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class caller client
    class issuer,signer,narrows,verifier,caveats role
    class cache,metering,static,remote optional
    class store,usage,keys,impl store
    class jwks,otel external
```

`Store` and `UsageStore` are separate types on purpose: both declare a `Get`
with different signatures, so one type cannot satisfy both. `TX` is the
consumer's transaction handle; the module only threads it back through
`Meter.Charge`, which is why it has no database dependency.

## Lifecycle of a capability

One orchestrator, one sub-agent, one service. Every call carries a token; the
service verifies it locally, enforces the caveats, then meters the call.

```mermaid
sequenceDiagram
    autonumber
    actor Op as Operator
    participant I as Issuer
    participant O as Orchestrator
    participant W as Sub-agent
    participant S as Service
    participant St as Store / UsageStore

    Op->>I: Issue(IssueRequest)
    I->>I: Caveats.Validate · TTL → ExpiresAt
    I->>St: Store.Insert(cap, IssuedBy)
    I->>I: Signer.Sign(cap)
    I-->>O: token

    O->>I: Delegate(DelegateRequest{Parent, Caveats})
    I->>St: Store.IsRevoked(parent)
    I->>I: Narrows(parent, child)
    I->>St: Store.Insert(child, parent.Subject)
    I-->>W: child token

    W->>S: request + child token
    S->>S: Verify(token, audience)
    S->>St: IsRevoked(child) — via CachedRevocationChecker
    S->>S: Caveats.CheckSource(addr) · Caveats.Check(op, resource)
    S->>St: BumpRequest · Charge, or Reserve then Settle
    St-->>S: ChargeReceipt{ChargeID, Spent}
    S-->>W: response

    Op->>St: Store.Revoke(RevokeArgs{ID: parent, CascadeChildren})
    Note over S,St: the next IsRevoked for the child answers true<br/>once the cache entry expires, or at once after Clear / Invalidate
    W->>S: request + child token
    S-->>W: ErrRevoked
```

## Verification gates

`StandardVerifier.Verify` reads nothing from the claims until the signature
over them has verified, and asks the revocation store last so that cheap
rejections never reach it. Every exit is a sentinel the caller maps to its own
transport status.

```mermaid
flowchart TB
    tok(["token, audience"])
    size{"len ≤ MaxTokenBytes"}
    hdr{"header parses<br/>typ = TokenType · kid set"}
    key{"KeyResolver.PublicKey(kid)"}
    sig{"Ed25519 signature"}
    claims{"claims parse"}
    iss{"iss ∈ TrustedIssuers<br/>KeyIssuers[kid] = iss"}
    tenant{"Subject.TenantID set"}
    nbf{"now + Leeway ≥ NotBefore"}
    exp{"now − Leeway ≤ ExpiresAt"}
    aud{"audience ∈ Audience"}
    rev{"RevocationLookup.IsRevoked<br/>this capability or an ancestor"}
    ok(["*Capability"])

    bad["ErrInvalidSignature"]
    early["ErrNotYetValid"]
    expired["ErrExpired"]
    mism["ErrAudienceMismatch"]
    revoked["ErrRevoked"]

    tok --> size -- yes --> hdr -- yes --> key -- found --> sig -- valid --> claims -- yes --> iss -- yes --> tenant -- yes --> nbf
    nbf -- yes --> exp -- yes --> aud -- yes --> rev -- no --> ok
    size -- no --> bad
    hdr -- no --> bad
    key -- "unknown kid" --> bad
    sig -- invalid --> bad
    claims -- no --> bad
    iss -- no --> bad
    tenant -- no --> bad
    nbf -- no --> early
    exp -- no --> expired
    aud -- no --> mism
    rev -- yes --> revoked

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class tok,ok client
    class size,hdr,sig,claims,iss,tenant,nbf,exp,aud role
    class key,rev store
    class bad,early,expired,mism,revoked external
```

An unknown kid, an untrusted issuer, a kid bound to another issuer and a
missing tenant all surface as `ErrInvalidSignature`, so security-sensitive
callers cannot branch on which one it was.

Verification proves the token is genuine. What the bearer may do with it is a
second, separate step, `Caveats.Check`, evaluated per operation:

```mermaid
flowchart LR
    req(["CheckRequest<br/>Op · Resource · HasIdempotencyKey · ResourceTainted"])
    op{"Op ∈ Ops"}
    res{"AllowsResource(Resource)<br/>prefix at a / boundary"}
    idem{"mutating Op and<br/>IdempotencyKeyRequired<br/>→ key present"}
    taint{"read of a tainted resource<br/>→ AllowTaintedRead"}
    ok(["nil"])

    e1["ErrOpNotAllowed"]
    e2["ErrResourceNotAllowed"]
    e3["ErrIdempotencyKeyRequired"]
    e4["ErrTaintedReadNotAllowed"]

    req --> op -- yes --> res -- yes --> idem -- yes --> taint -- yes --> ok
    op -- no --> e1
    res -- no --> e2
    idem -- no --> e3
    taint -- no --> e4

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class req,ok client
    class op,res,idem,taint role
    class e1,e2,e3,e4 external
```

Each of the four also matches `ErrCaveatViolation`. The source-address caveat
is per connection and checked by `Caveats.CheckSource`, which fails closed on
an unknown address with `ErrSourceIPNotAllowed`. Budget and request ceilings
are stateful and belong to the `UsageStore`.

## Delegation: attenuate, never escalate

`Issuer.Delegate` refuses a parent that is expired or revoked, clamps the
child's expiry to the parent's, and then requires `Narrows(parent, child)` to
pass before anything is persisted or signed.

```mermaid
flowchart TB
    req(["DelegateRequest<br/>Parent · Subject · Caveats or InheritCaveats · TTL"])
    valid{"Caveats.Validate<br/>audience non-empty"}
    alive{"parent not expired<br/>Store.IsRevoked(parent) = false"}
    clamp["ExpiresAt = min(now + TTL, parent.ExpiresAt)"]

    subgraph nar ["Narrows(parent, child)"]
        direction TB
        n1["Ops ⊆ parent.Ops"]
        n2["every resource reachable by the parent<br/>exact-URI parent admits no child prefix"]
        n3["MaxRequests ≤ parent's, never unlimited under a bounded parent"]
        n4["UnitCode = parent's"]
        n5["MaxBudgetAmount ≤ parent's, never unlimited under a bounded parent"]
        n6["AllowTaintedRead only if the parent has it<br/>IdempotencyKeyRequired never relaxed"]
        n7["SourceIPCIDR inside a parent network"]
        n1 --> n2 --> n3 --> n4 --> n5 --> n6 --> n7
    end

    persist["Store.Insert(child, issuedBy = parent.Subject)<br/>Signer.Sign(child)"]
    ok(["child, childToken"])

    wide["ErrDelegationTooWide"]
    unit["ErrUnitCodeMismatch"]
    dead["ErrExpired · ErrRevoked"]
    inval["ErrInvalidCaveats · audience error"]

    req --> valid -- yes --> alive -- yes --> clamp --> n1
    n7 --> persist --> ok
    valid -- no --> inval
    alive -- no --> dead
    n4 -- differs --> unit
    nar -- widens --> wide

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class req,ok client
    class valid,clamp,n1,n2,n3,n4,n5,n6,n7 role
    class alive,persist store
    class wide,unit,dead,inval external
```

Narrowing bounds each child on its own. The tree as a whole is bounded at use:
every charge also counts against each ancestor, read from the ancestor's
stored record. A parent holding 25.00 that delegates 20.00 to each of two
workers can still spend 25.00 in total, not 40.00:

```mermaid
flowchart TB
    p["<b>orchestrator</b><br/>MaxBudgetAmount 25.00<br/>spent 25.00 = 15.00 + 10.00"]
    a["<b>worker A</b><br/>MaxBudgetAmount 20.00<br/>spent 15.00"]
    b["<b>worker B</b><br/>MaxBudgetAmount 20.00<br/>spent 10.00"]
    next(["worker B charges 1.00 more"])
    deny["ErrBudgetExceeded: ancestor<br/>B's own ceiling allows it, the parent's does not"]

    p -- "ParentID" --- a
    p -- "ParentID" --- b
    next --> b
    b -. "ancestor ceiling 25.00 + 1.00" .-> deny

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class next client
    class p,a,b role
    class deny external
```

## Charge: stage, then commit

`Meter.Charge` checks three ceilings and runs the consumer's side effect before
any counter moves. A rejection at any stage, or a failing `onCharged`, leaves
the capability's counter, every ancestor's, the tenant aggregate and the
ledger exactly as they were, so a retry after a rejection is safe.

A cost known only afterwards goes through a reservation instead. `Reserve`
holds an estimate against the same three ceilings, and `Settle` charges the
actual cost along the same path, releasing the hold in the same commit. Every
ceiling counts open holds as well as spend, so two callers cannot both hold
the last of a budget.

```mermaid
flowchart TB
    req(["ChargeRequest<br/>CapabilityID · TenantID · Amount · MaxBudget · UnitCode"])
    sreq(["SettleRequest<br/>ReservationID · Amount · MaxBudget"])
    hold{"reservation open"}
    val{"ValidateAmount<br/>NormaliseUnitCode"}

    subgraph stage ["stage — compute, publish nothing"]
        direction TB
        c1{"own spend + reserved + Amount ≤ MaxBudget<br/>(ceiling from the verified token)"}
        c2{"each ancestor: spend + reserved + Amount<br/>≤ its MaxBudgetAmount<br/>(ceiling from the stored record)"}
        c3{"tenant spend + reserved + Amount<br/>≤ TenantBudget.MaxBudgetAmount"}
        cb{"onCharged(ctx, tx)<br/>consumer's side effect on its own TX"}
        c1 -- yes --> c2 -- yes --> c3 -- yes --> cb
    end

    subgraph commit ["commit — all together"]
        direction TB
        w1["capability spend += Amount"]
        w2["each ancestor spend += Amount"]
        w3["tenant spend += Amount"]
        w0["settling: the hold leaves reserved everywhere"]
        w4["ledger row: ChargeID · Ancestors · Op · Actor"]
    end

    ok(["ChargeReceipt{ChargeID, Spent}"])
    refund["Refund(ChargeID, Amount)<br/>returns spend to every counter the charge took it from"]

    e0["ErrInvalidAmount · unit error"]
    e1["ErrBudgetExceeded"]
    e2["ErrBudgetExceeded: ancestor"]
    e3["ErrTenantBudgetExceeded"]
    e4["the callback's error"]
    e5["ErrReservationNotFound"]

    req --> val -- ok --> c1
    sreq --> hold -- "yes: its own hold<br/>is not counted" --> c1
    hold -- "settled · released · expired" --> e5
    cb -- nil --> w1 --> w2 --> w3 --> w0 --> w4 --> ok
    ok -. "later" .-> refund
    val -- invalid --> e0
    c1 -- no --> e1
    c2 -- no --> e2
    c3 -- no --> e3
    cb -- error --> e4

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class req,sreq,ok client
    class val,hold,c1,c2,c3 role
    class cb optional
    class w1,w2,w3,w0,w4,refund store
    class e0,e1,e2,e3,e4,e5 external
```

A settle rejected by a ceiling leaves the reservation in place, to be settled
lower or released with `Release`. A hold never settled lapses at its
`ExpiresAt`, but keeps counting until `ReleaseExpired` runs, which errs towards
refusing spend rather than allowing too much.

A store with transactions runs stage and commit inside one, and passes its own
handle as `tx`; a store without them is instantiated as
`UsageStore[struct{}]` and the caller passes `nil` for `onCharged`.

## Revocation reaches the verifiers

Revocation is a write to the `Store`; verifiers see it through
`IsRevoked`, which answers for the whole delegation chain, usually behind a
`CachedRevocationChecker`.

```mermaid
flowchart LR
    op(["Revoke(RevokeArgs{ID, Reason, Actor, CascadeChildren})"])
    store[("<b>Store</b><br/>revoked set<br/>ParentID links")]
    cascade["CascadeChildren:<br/>a revocation entry per descendant,<br/>for the audit trail"]
    chain["IsRevoked(id):<br/>id or any ancestor revoked"]
    cache["<b>CachedRevocationChecker</b><br/>answer cached for TTL<br/>one upstream call per id at a time<br/>errors never cached"]
    notify{{"consumer's revocation signal<br/>e.g. a Postgres channel"}}
    v1["StandardVerifier"]
    v2["StandardVerifier"]

    op --> store
    store -.-> cascade
    store --> chain --> cache
    cache --> v1
    cache --> v2
    notify -. "Clear() · Invalidate(id)" .-> cache

    classDef client fill:#E0F2FE,stroke:#0284C7,color:#0C4A6E
    classDef role fill:#DCFCE7,stroke:#16A34A,color:#14532D
    classDef optional fill:#F1F5F9,stroke:#64748B,color:#334155,stroke-dasharray:5 4
    classDef store fill:#FEF3C7,stroke:#D97706,color:#78350F
    classDef external fill:#FCE7F3,stroke:#DB2777,color:#831843
    class op client
    class v1,v2,cache role
    class cascade optional
    class store,chain store
    class notify external
```

Revoking a capability stops everything delegated from it whether or not
`CascadeChildren` is set, because `IsRevoked` walks the ancestors; the flag
only adds the per-descendant entries that name each stopped capability. Without
a revocation signal, a verifier learns of a revocation within the cache TTL.

A verifier that runs apart from the issuer resolves keys with
`RemoteJWKSResolver`: it serves the cached key set for `RefreshInterval`,
revalidates with an ETag, refetches on an unknown kid at most once per
`MinRefreshInterval`, and fails closed with `ErrJWKSUnavailable` once the last
good set is older than `MaxStale`.
