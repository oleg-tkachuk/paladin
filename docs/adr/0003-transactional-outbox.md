# ADR-0003: Transactional event outbox

- **Status:** Accepted — implemented for the promote path (2026-06);
  remaining lifecycle events + the testcontainers crash-window test are
  follow-up (see "Status" at the end).
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

## Status of implementation (2026-06)

Landed:
- `statemachine.Transitioner.PromoteToAvailableInTx(…, onPromoted func(ctx,
  pgx.Tx) error)` — promote + callback in one tx; promote core refactored
  to run on a `dbExec` (pool **or** tx).
- `worker.Dispatcher.DispatchTx(ctx, tx, tenantID, evt)` + shared
  `dispatch(insert)` + `insertOutboxRow(exec, row)` so the fan-out writes
  on the caller's tx.
- `SoftDeleteInTx` / `RestoreInTx` orchestrators (sharing a
  `transitionInTx(do, after)` helper) cover the AVAILABLE→DELETED and
  DELETED→AVAILABLE transitions the same way.
- All three statemachine-driven object events are now transactional:
  - `object.CompleteObject` → `paladin.object.uploaded` (promote)
  - `object.DeleteObject` (soft) → `paladin.object.deleted`
  - `object.RestoreObject` → `paladin.object.restored`
  via a shared `Handler.dispatchEventTx` helper. A dispatch error rolls
  the transition back (the client's at-least-once retry re-runs both).
  Version-history / quota / capability-charge stay post-commit (separate
  concerns; a hiccup there must not roll back a delivered event).
- Unit tests: `DispatchTx` writes one row per matching sub on the tx
  (not the pool), skips disabled subs, and a nil-Outbox dispatcher works.

Follow-up (tracked in BACKLOG):
- The two **repo-based** mutations still use best-effort `Dispatch`:
  `paladin.object.updated` (UpdateMetadata) and the permanent-delete
  `paladin.object.deleted` (HardDelete + the post-commit S3 delete). They
  need a repo-level tx seam rather than the statemachine orchestrator —
  separate change. Same for the admin-plane lifecycle events
  (tenant/bucket/objectKey/quota) and the event-ingest promote path.
- A testcontainers integration test asserting the crash window (commit
  state, kill before fan-out → row present after recovery) and rollback
  (dispatch error → no state change, no outbox row).
