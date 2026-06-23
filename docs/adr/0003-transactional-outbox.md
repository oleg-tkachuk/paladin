# ADR-0003: Transactional event outbox

- **Status:** Accepted — fully implemented (2026-06). Every lifecycle
  event across the data plane (promote, soft-delete, restore, update,
  permanent-delete, copy), the event-ingest promote path, AND the
  admin plane (tenant, object_key, bucket, quota) now writes its outbox
  rows in the producing transaction. A testcontainers crash-window test
  asserts the invariant on real Postgres. No remaining follow-up.
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
- All five data-plane object events are now transactional:
  - `object.CompleteObject` → `paladin.object.uploaded` (promote)
  - `object.DeleteObject` (soft) → `paladin.object.deleted`
  - `object.RestoreObject` → `paladin.object.restored`
  - `object.UpdateObject` → `paladin.object.updated`
  - `object.DeleteObject` (permanent) → `paladin.object.deleted`
  via a shared `Handler.dispatchEventTx` helper. A dispatch error rolls
  the transition back (the client's at-least-once retry re-runs both).
  Version-history / quota / capability-charge stay post-commit (separate
  concerns; a hiccup there must not roll back a delivered event).
- The two **repo-based** mutations (`UpdateMetadata`, `HardDelete` /
  `HardDeleteWithBypass`) gained a repo-level seam rather than the
  statemachine orchestrator: `ObjectRepo.RunInTx(fn)` opens one tx on the
  pool, and `UpdateMetadataTx` / `HardDeleteTx` / `HardDeleteWithBypassTx`
  run the mutation on that tx (the bypass variant sets
  `SET LOCAL paladin.governance_bypass` on the same tx). The handler does the
  mutation + `dispatchEventTx` inside the closure. For permanent delete
  the S3 byte-removal stays **after** commit (S3 is non-transactional and
  the DB-then-S3 ordering must hold): the event is enqueued atomically
  with the row removal, then the bytes are reclaimed best-effort.
- Unit tests: `DispatchTx` writes one row per matching sub on the tx
  (not the pool), skips disabled subs, and a nil-Outbox dispatcher works.

- The event-ingest promote path now also enqueues `paladin.object.uploaded`
  on the promote tx: `eventingest.PromoteHandler` gained a producer-only
  `worker.Dispatcher` (bound to the ingest BYPASSRLS queries), so an
  explicit-mode storage event notifies webhook subscribers exactly like an
  implicit-mode CompleteObject does. The `changed` guard keeps emission
  exactly-once across the two producers. Same for the synchronous
  server-side CopyObject promote.
- Integration test: `internal/integration/outbox_crash_test.go`
  (`-tags=integration`, testcontainers Postgres) asserts the invariant on
  a live DB for both seams — the statemachine (`PromoteToAvailableInTx`)
  and the repo (`RunInTx` + `HardDeleteTx`): a committed transition carries
  its outbox row, and a dispatch error rolls back BOTH the state change and
  the already-inserted outbox row (no committed-but-unenqueued window).

- The admin-plane lifecycle events (tenant created/updated/trashed/
  restored/purged, object_key created/updated/deleted, bucket created/
  updated/deleted/deleting, quota set) now go through the same seam. The
  two query-only admin repos (`BucketRepoV2`, `QuotaRepoV2`) gained a pool
  + `RunInTx`; the bucket/quota handlers hold a local `Repository` interface
  (domain interface + the `*Tx` methods) so `admindomain` stays pgx-free.
  Bucket/quota read the owner tenant_id back on the tx (the fan-out target
  lives only on the stored row). `paladin.bucket.deleting` still fires `.deleting`
  (the reconciler later removes the row); the reconciler emitting a terminal
  `.deleted` on completion remains a separate BACKLOG item.

No remaining follow-up — the dual-write crash window is closed for every
producer.
