# ADR-0037: Revocation answers are cached for as long as the revocation signal vouches for them

- **Status:** Accepted. Implemented 2026-10-22 (#740, limes v0.116.0).
- **Related:** builds on [ADR-0021](0021-biscuit-copies.md) (Biscuit copies
  revoked by block) and [ADR-0031](0031-capability-verify-then-handler.md)
  (the verifier in the interceptor); constrained by
  [ADR-0007](0007-postgres-connection-headroom.md) (connection pooling).

- **Context.** Every request carrying a capability or a Biscuit copy asks
  `IsRevoked` for it and for each ancestor. Without a cache, each one costs
  a Postgres lookup per request. With a cache, an answer outlives the
  revocation that outdates it, and a revoked capability, which lives up to
  `default_ttl` (15m), keeps working on that replica until the answer
  expires.

  The cache used one TTL, `revocation_cache_ttl`, set to 2s. That kept
  staleness low, but an agent that checks one token once per model step,
  several seconds apart, missed the cache on almost every check.

  Meanwhile the revocations got their own signal. pgstore's trigger calls
  `pg_notify('limes_revocations', …)` in the transaction that revokes, with
  the ids it revoked. Every replica's watcher `LISTEN`s and clears those
  answers and their descendants' (`ClearRevoked`, by lineage). A revocation
  made anywhere, an operator's `psql` included, reaches each replica as soon
  as the transaction commits. So a short TTL guards the cache only while no
  watcher is listening.

- **Decision.**
  1. **Two TTLs, chosen by the signal's state.**
     - `revocation_cache_synced_ttl` (default 30s) applies while the watcher
       listens.
     - `revocation_cache_ttl` (default 2s) applies before its first `LISTEN`
       and from the moment it loses its connection until it listens again.
     The watcher tells the cache both transitions (`pgstore.Syncer`).
  2. **Entering sync clears the cache.** An answer cached before the
     `LISTEN` may predate a revocation no notification will repeat. Losing
     the connection cuts every cached answer back to the short TTL at once,
     not when it next expires.
  3. **The synced TTL is never below the TTL.** limes refuses that. Paladin
     raises the synced TTL to the TTL instead, so a deployment that already
     raised the TTL keeps starting and gets the behaviour it had.
  4. **Lineage is required** (`RequireLineage`). A store decorator that drops
     `RevocationLineage` fails startup. Otherwise every revocation would turn
     into a clear of the whole cache, without anything reporting it.
  5. **The signal is observable.** The health page has a non-critical
     capability-revocations row: down before the first `LISTEN` and after a
     lost connection, with when it was lost and the connection and
     notification counts. otelmetric exports the same counts.

- **Options not taken.**
  - **One short TTL (2s), as before.** It bounds staleness without trusting
    the signal, but it pays a lookup on nearly every agent step, and the
    notification already gives the bound it buys.
  - **One long TTL with notifications only.** Staleness would be unbounded
    for as long as a watcher is disconnected and does not know it.
  - **No cache, a lookup per request.** This is the simplest correct option,
    and it puts a Postgres round trip on the hot path of every plane call.

- **Consequences.**
  - **Staleness while the signal is up:** a revocation goes unseen on a
    replica only for the delivery time of one notification. The 30s TTL is
    the bound only for a notification that never arrives on a connection the
    watcher still believes is live.
  - **Staleness while the signal is down:** at most 2s, from the moment the
    watcher notices.
  - **The watcher holds one connection per replica**, hijacked from the
    pool, for the life of the process.
  - **It needs a session-mode connection.** `LISTEN` does not survive
    transaction pooling. If PgBouncer in transaction mode fronts the
    cluster (ADR-0007 leaves that to gitops), the watcher's connection must
    bypass it. Otherwise the watcher listens to nothing while the cache
    serves the 30s TTL.
  - **Tuning is per deployment.** Raising the synced TTL buys hits at the
    cost of the undelivered-notification window. Raising the TTL widens the
    window while the signal is down. The health row, not the TTL, is what
    says which regime a replica is in.
