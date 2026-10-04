# Runbook: uploads the reconciler cannot settle

Covers `PaladinUploadsNotSettling` in
[`deploy/grafana/paladin-alerts.yaml`](../../deploy/grafana/paladin-alerts.yaml).

| Fires when | Meaning |
|-----------|---------|
| `max(paladin_objects_pending_overdue_age_seconds) > 1800` for 15m | A PENDING object has been past the reconciler's deadline for over 30m. |

An upload stays `PENDING` until the client's `CompleteObject`, a storage
event, or the reconciler settles it. The reconciler takes every `PENDING`
object whose presign expired more than `worker.jobs.reconciler.min_object_age`
ago — oldest first, `batch_size` per `interval` — HEADs its bytes, and
promotes it to `AVAILABLE` or fails it. Until then the object cannot be
downloaded, and its client may be waiting on it.

The worker alerts do not see this. `reconcile()` logs a HEAD or promote that
failed and moves to the next object, so the tick succeeds:
`PaladinWorkerTicksAllFailing` stays quiet while nothing is settled. A
reconciler that is not running at all is `PaladinWorkerStalled`'s
([worker-stalled.md](worker-stalled.md)), and one disabled by config
(`interval <= 0`) emits neither signal.

## Signal source

The reconciler samples after every tick (`internal/worker/reconciler.go`,
`statemachine.PendingOverdue`):

- `paladin_objects_pending_overdue` — PENDING objects past their deadline.
- `paladin_objects_pending_overdue_age_seconds` — how far past it the oldest
  is; 0 with none.

A reconciler keeping up holds the age under one `interval`. The *Overdue
uploads* panel on the Operations dashboard shows both.

## Triage

1. **Is the count falling?** A backlog larger than one batch — after a
   storage outage, or a burst of abandoned uploads — drains at `batch_size`
   per `interval`. A count that falls steadily needs only time; raise
   `worker.jobs.reconciler.batch_size` to drain it faster.

2. **What does the reconciler log?** Its lines carry `object_id`:

   ```
   kubectl --context=<ctx> -n paladin logs deploy/paladin-core-worker --tail=500 | grep -E 'failed to (HEAD|promote|mark|delete mismatched|scan|sample)'
   ```

   | Line | Cause |
   |------|-------|
   | `failed to HEAD object` | The backend did not answer: down, unreachable from the worker, or refusing its credentials. The object is left PENDING on purpose — an unanswered HEAD must not fail a live upload. |
   | `failed to promote object` | Postgres refused the transition. |
   | `failed to mark object as failed` | As above, for an upload whose bytes never arrived. |
   | `failed to delete mismatched object bytes` | Stored bytes broke the object's registration and could not be removed; the row waits for that. |
   | `failed to scan pending objects` | The reconciler cannot read `objects` at all. |

3. **Which backend?** Overdue objects by collection, the oldest first:

   ```sql
   SELECT c.name AS collection, count(*) AS overdue, min(o.presign_expires_at) AS oldest_expiry
     FROM objects o JOIN collections c ON c.id = o.collection_id
    WHERE o.state = 'PENDING'
      AND o.presign_expires_at < now() - interval '2 hours'  -- min_object_age
    GROUP BY c.name ORDER BY overdue DESC LIMIT 20;
   ```

   Collections on one backend point at that backend; check its health and
   that the worker reaches its endpoint.

## Mitigation

Fix the cause the logs name — the backend, the worker's route to it, its
credentials, the database. Every reconcile is idempotent, so the next ticks
settle the backlog with nothing replayed by hand, and the alert clears once
the age is back under 30m.

## Escalation / notes

- `warning`, not a page: no data is lost — the bytes are in storage or never
  arrived, and the row says which once the reconciler can look.
- Clients see these objects as PENDING. A client retrying an upload to the
  same key meets `AlreadyExists`; the SDK cookbook's durable upload
  (`Example_durableUpload`, `examples/durable_upload.py`) calls
  `CompleteObject` on such an object itself.
