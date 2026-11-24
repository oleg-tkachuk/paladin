## Unreleased — a storage migration completes once its URLs have expired

- A migration no longer waits `limits.presign.max_ttl` plus
  `worker.jobs.reconciler.min_object_age` after it starts moving. It
  completes once nothing of the tenant is placed in or anchored to the
  source, no multipart session is open there and nothing is bound to it:
  usually a few minutes after the last upload URL signed for the source
  expires. A pending upload to the source still holds it until it completes
  or the reconciler discards it, `min_object_age` past its URL's expiry.
- New key `worker.jobs.reconciler.write_settle_grace` (default `5m`): how
  long past its URL's expiry a promoted object's upload anchor is settled.
  It used to be `min_object_age`, which stays the grace of a pending upload.
  Raise it with `limits.max_object_size` if clients upload slowly.
- New key `worker.jobs.storage_migration.source_copy_retention` (default
  `limits.presign.max_ttl`): the least a completed migration keeps the
  source copies, for GET URLs signed before their objects moved. The
  operator's `cleanup_retention_seconds` can only lengthen it.
- A completed migration keeps passing until it is cleaned: an upload a
  request anchored to the source after the completion is moved, a
  collection bound there is pointed at the target, and the cleanup waits for
  them. Regenerated and part URLs for the source stay bounded by the write
  deadline until the migration is cleaned.
- No key is removed. Ship the schema with the binary: the loader refuses the
  new keys before it.
