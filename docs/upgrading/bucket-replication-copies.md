## Unreleased — bucket replication copies objects

- With `worker.jobs.replication.enabled: true`, the replication worker now
  copies each replicated bucket's new objects to its
  `replication.destination_bucket`, on the same backend or another. It used to
  log what it would copy and copy nothing. Check each bucket with
  `replication.enabled` before deploying: its destination must be a
  registered bucket, ready, on an enabled backend that is not draining, or
  the bucket is deferred every tick.
- Only objects committed within `lookback_window` of the first pass are
  copied; older ones are not backfilled.
- `SetReplication` refuses a `filter` that does not compile as CEL, with
  `INVALID_ARGUMENT`. A stored filter that does not compile stops its
  bucket's replication, logged at ERROR, until it is fixed.
- No configuration changes. See ADR-0050.
