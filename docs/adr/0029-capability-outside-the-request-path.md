# ADR-0029: Capabilities for work the verifier does not see

- **Status:** Accepted — implemented 2026-10-08.
- **Related:** [ADR-0021](0021-biscuit-copies.md) (Biscuit copies),
  [ADR-0002](0002-api-error-connect-mapping.md) (error reasons).

- **Context.** The module assumed the verifier sits in the path of every
  operation and every cost: it knows what each operation does, it charges a
  cost as the request is served, and its consumer decides what a refusal looks
  like on the wire. An agent breaks all three. Its tools are the consumer's own
  operations, of which the module knows nothing, so every one was a write:
  a read-only tool needed an idempotency key and was never refused as a tainted
  read. Its model calls go to a provider directly, and their price arrives
  later, on a stream delivered at least once — a repeat was charged twice, and
  a cost past the ceiling was refused although it had already been spent. And
  its client could not tell a capability that is spent or revoked, which it
  should stop using, from a request it can fix.

- **Decision.**
  1. **The server declares an operation's effect.** `CheckRequest.Effect` is
     read or write, declared where the consumer defines the operation, never
     by the bearer. A built-in operation keeps its own effect, and a
     contradicting declaration is `ErrEffectConflict` — a write declared a
     read would shed `IdempotencyKeyRequired`. Undeclared, a consumer-defined
     operation stays a write. Anything else gated on the effect asks
     `CheckRequest.Mutating`, so it and `Check` cannot disagree.
  2. **A cost reported later is charged once, and charged even past a
     ceiling.** `ChargeRequest.ExternalRef` names the cost in the consumer's
     records; a capability is charged once per name, and a repeat returns the
     first receipt with `Replayed`. `Settle` is idempotent on its reservation
     the same way. `OverrunRecord` commits an incurred cost past every ceiling
     it crosses and says so in the receipt; the crossed ceiling then refuses
     what follows under the default `OverrunReject`, which is the one that
     stops a cost not yet incurred. A recorded overrun is an accepted charge,
     not an error, so a rejected charge still moves nothing.
  3. **A refusal has a reason, and the reason says whether it is permanent.**
     `ReasonOf` names each refusal the verifier, `Check` or a `Meter`
     returns; `Reason.Permanent` holds when the same request with the same
     capability can never pass. Paladin attaches it as an
     `ERROR_REASON_CAPABILITY_*` reason, spelled from the module's by one
     rule; the Connect codes are unchanged.

- **Consequences.**
  - A request field a store ignores still compiles, so the contract is
    checked rather than trusted: `capability/metertest` runs it against any
    `Meter`, as `testing/fstest` does for a file system, and both stores here
    run it. `Meter` also gains `ChargeByRef`, which reads a named charge back,
    so a store written before this change stops compiling until it keeps
    external refs.
  - The ledger gains `external_ref`, `reservation_id` and `overrun`, each
    unique where set (migrations `051`–`053`); repeated charges of one name
    are serialised on an advisory lock with the index as the backstop.
  - A cost below a micro still rounds to zero one charge at a time; a
    consumer with many of them sums them first. Exact sub-micro accounting
    belongs with the move to integer amounts (BACKLOG).
  - Not taken: tools as a caveat of their own (namespaced operations already
    name them, without a change to the token format); a library-side cost
    estimator (the consumer prices its own calls); a reserve allowed to run
    past the ceiling for a final call (a cost already incurred is recorded by
    `OverrunRecord`, one not yet incurred should be refused).
