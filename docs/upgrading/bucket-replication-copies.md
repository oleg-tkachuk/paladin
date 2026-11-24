## Unreleased — bucket replication copies objects

- With `worker.jobs.replication.enabled: true`, the replication worker now
  copies each replicated bucket's objects to its
  `replication.destination_bucket`, on the same backend or another. It used to
  log what it would copy and copy nothing. Check each bucket with
  `replication.enabled` before deploying: its destination must be a
  registered bucket, ready, on an enabled backend that is not draining, or
  the bucket is deferred every tick.
- The first pass over a replicated bucket copies every `AVAILABLE` object in
  it, however old; so does the first pass after its destination or filter
  changes. Expect that traffic on the first deploy.
- An object rewritten in place is copied again.
- A purged object's replica is deleted from the destination — a permanent
  delete, the trash's expiry, or its tenant's purge. A move to the trash
  keeps the replica.
- `worker.jobs.replication.lookback_window` is removed; delete it from any
  values or config that set it, since the strict loader refuses an unknown
  key.
- `SetReplication` refuses a `filter` that does not compile as CEL, with
  `INVALID_ARGUMENT`. A stored filter that does not compile stops its
  bucket's replication, logged at ERROR, until it is fixed.
- Migrations 119 and 120 add triggers on `objects` and `buckets` and drop
  `replication_state`. See ADR-0050.
