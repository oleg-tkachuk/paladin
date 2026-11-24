## Unreleased — a storage migration moves objects one by one

- A tenant's storage migration no longer rebinds every collection at one
  instant (ADR-0051). Once its dedicated bucket is ready it enters the new
  state `moving`: the tenant's private collections and default binding point
  at the target and `storage_layout` is `dedicated` at once, so new uploads
  go there, and each pass of the worker copies the objects still in the
  source, verifies each copy and moves its placement. `rebinding` and
  `verifying` are gone; migration 127 resumes a migration found in
  `copying`, `rebinding` or `verifying` as `moving`.
- A migration completes only once nothing of the tenant is left in or
  anchored to the source. The source copies are then kept until the later of the retention the
  operator asked for and `worker.jobs.storage_migration.source_copy_retention`
  (by default `max_ttl`), so a GET URL signed before an object
  moved keeps working until it expires.
- A rollback moves the objects back the same way. It starts when the target
  bucket leaves `ready`, the schema refuses to move a collection, or moves
  fail their verification on five passes in a row; the migration ends
  `failed`, the tenant `shared`, and is retried with
  `MigrateTenantStorageLayout` as before. `objects_copied` counts the moves
  of the current direction, `objects_total` the moves plus what is still to
  move.
- An upload's anchor (the bucket its URL was signed for) is kept after its
  promote until the URL has expired; the reconciler then HEADs the bucket
  once, clears the anchor, and records any write through the URL that no
  storage event reported. A storage event is applied only when it comes
  from that bucket; one from any other bucket is acknowledged and changes
  nothing. Migration 127 anchors every pending upload and every promoted
  object whose URL is still valid; 128 indexes the anchors by URL expiry
  concurrently. Moving an object or clearing its anchor no longer changes
  its `resource_version` (migration 129).
- `CompleteObject`, a multipart completion and the reconciler promote an
  upload in the bucket its URL was signed for and no longer copy it into
  the collection's current bucket. A regenerated upload URL is signed for
  that same bucket. While a migration empties a bucket, a regenerated or
  part URL for it expires by the migration's write deadline, and past it is
  refused with `FAILED_PRECONDITION` ("start a new upload").
- `CreateCollection` no longer refuses a collection bound to a bucket a
  migration has left (`ABORTED`); the migration's next pass moves it.
