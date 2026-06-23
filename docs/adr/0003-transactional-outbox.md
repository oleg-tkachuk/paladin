# ADR-0003: Transactional event outbox

- **Status:** Proposed (design + plan; not yet implemented)
- **Context:** `event_deliveries` is the durable webhook outbox. Today the
  producer writes outbox rows AFTER the state transition has already
  committed: a handler calls `h.sm.PromoteToAvailable(...)` (its own
  single-statement auto-commit tx), and only then
  `dispatcher.Dispatch(ctx, tenantID, evt)` inserts the rows. A crash
  between the state commit and the outbox insert loses the event
  permanently — the classic dual-write problem. For implicit-mode buckets
  (S3 events off) the webhook is the only notification channel, so this is
  silent under-delivery, not just a delay.

## Decision

Make the outbox insert atomic with the state transition by writing both
in **one** Postgres transaction. The dispatcher's HTTP/NATS delivery
stays asynchronous (the dispatcher pod drains the outbox via FOR UPDATE
SKIP LOCKED) — only the *enqueue* becomes transactional.

## Implementation plan

1. **statemachine seam.** Add tx-aware variants of the transition methods
   used by event-producing handlers (`PromoteToAvailable`, `SoftDelete`,
   `Restore`, …) — e.g. `…Tx(ctx, tx pgx.Tx, …)` — leaving the current
   pool-based methods as thin wrappers that open + commit their own tx
   (no behaviour change for callers that don't need atomicity).
2. **dispatcher seam.** `Dispatcher.Dispatch` (and `OutboxWriter.Insert`)
   accept an optional `pgx.Tx`; when present, insert on it instead of the
   pool.
3. **handler.** The producing handlers open one `pool.Begin`, run the
   transition + the outbox fan-out on that tx, then commit. On any error,
   rollback drops both — no half-state, no orphan event.
4. **Tenant scoping.** Keep the existing tenant-scoped subscription query
   (fan-out is already tenant-filtered).

## Consequences

- Exactly-"state-changed ⇒ event-enqueued" within the producing tx; the
  crash window closes. Delivery is still at-least-once (consumers already
  dedup on event id), unchanged.
- Blast radius: the core promote/delete/restore path + the dispatcher
  enqueue API. Requires a testcontainers integration test asserting the
  crash window (commit state, kill before fan-out → row present after
  recovery) and rollback (transition error → no outbox row, no state
  change). That test harness + the cross-package signature change is why
  this is its own focused PR rather than bundled with smaller fixes.
- Alternative considered & rejected: a trigger/`LISTEN`-`NOTIFY`
  transactional-outbox on the `objects` table. Rejected for now — it
  couples event semantics to table triggers and is harder to evolve than
  an explicit tx-threaded insert, though it remains a viable fallback if
  threading the tx proves too invasive.
