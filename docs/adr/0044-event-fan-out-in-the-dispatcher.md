# ADR-0044: An event is fanned out by the dispatcher, not in the producer's transaction

- **Status:** Accepted 2026-11-08.
- **Related:** [ADR-0003](0003-transactional-outbox.md) (the outbox; its
  atomicity is kept), [ADR-0004](0004-table-backed-audit-outbox.md) (the
  audit row the mirror shares a transaction with).

## Context

ADR-0003 made the outbox write atomic with the state change it reports. It
did so by running the whole fan-out inside the producer's transaction:
`Dispatcher.dispatch` listed every subscription of the tenant, evaluated
each CEL filter, and inserted one `event_deliveries` row per match. That
placed work proportional to the tenant's subscriptions inside every
event-producing transaction — a data-plane upload, a capability charge
holding the tenant's budget row, the audit row of every mutation when
`audit_mirror_enabled` is on — and it brought defects of its own:

- **A second connection inside the transaction.** Subscriptions were listed
  through the pool, not on the transaction, so each fan-out held one
  connection and waited for another. With every connection held by a
  transaction that has reached its fan-out, the pool waits on itself.
- **A swallowed insert error.** A failed `event_deliveries` insert was
  logged and skipped, and `DispatchTx` reported success. Postgres had
  already aborted the transaction, so the caller's commit failed with a
  rollback that named nothing.
- **A race with deleting a subscription.** A subscription deleted between
  the listing and the insert failed the foreign key, and with it the upload
  or state change that produced the event.
- **A lost audit row.** The audit mirror shares the audit row's
  transaction, which runs after the RPC committed; a failed fan-out rolled
  back the audit row of a mutation that had already happened.
- **Platform events failed their producer.** An event with no tenant (a
  storage backend's credential rotation) failed to parse the empty tenant
  id, which rolled the rotation back.

## Decision

The producer records the event, once; the dispatcher fans it out.

- **In the producer's transaction:** one row in `event_outbox` — tenant,
  type, time, payload. No subscription is read, no filter evaluated, no
  second connection taken; the cost does not depend on how many
  subscriptions the tenant has. Atomicity with the state change is
  unchanged: the event commits with it or not at all.
- **An event with no tenant** has no subscription that could match (every
  subscription belongs to a tenant), so the producer records nothing and
  says so in its log.
- **In the dispatcher:** each tick first claims a batch of `event_outbox`
  rows (`FOR UPDATE SKIP LOCKED`), reads the subscriptions of their
  tenants, evaluates the filters, inserts the `event_deliveries` rows and
  deletes the claimed events — one transaction, so a dispatcher that dies
  mid-batch leaves the events to be fanned out again, never half of them.
  Delivery from `event_deliveries` is unchanged.

### Which subscriptions receive an event

A subscription receives an event when, at fan-out, it exists, is enabled,
its filter matches, and it was created no later than the event was
recorded (both times from the database clock). Before, the set was the
subscriptions visible to the producer's listing; the two differ only for a
subscription created or deleted in the second between recording and
fan-out. A subscription deleted in that window receives nothing — as
before, when its queued rows went with it by cascade.

The delivery id stays what it was: the `event_deliveries` row id, stable
across retries, the dedup key of
[event-delivery-dedup.md](../event-delivery-dedup.md).

## Consequences

- An event-producing transaction writes one row whatever the number of
  subscriptions; the defects above have no code left to live in.
- `event_outbox` accepts an insert for any tenant: a platform admin acting
  on a tenant's resources records that tenant's event from a session pinned
  to its own, and the producer takes the tenant from the changed row, never
  from the request. Reading and deleting stay pinned to the session tenant.
- An event reaches its sink one stage later: fan-out and delivery run in
  the same tick, so the added latency is at most one poll interval.
- A backlog can now sit in `event_outbox` as well as in `event_deliveries`;
  the dispatcher logs each fan-out batch and warns, with the outbox depth,
  when the backlog not yet fanned out grows deep.
- The producer no longer knows how many subscriptions matched. Logs that
  reported it now report that the event was recorded; the count is logged
  by the dispatcher at fan-out.
