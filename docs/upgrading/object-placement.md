## Unreleased — an object is read from the bucket its bytes are in

- Every object now records the bucket its bytes are in
  (`objects.placement_bucket_id`, ADR-0051). Migration 123 adds the column,
  124 backfills it from each object's collection binding, and 125 builds an
  index on it concurrently. A pending upload is placed nowhere until its
  bytes are confirmed.
- Downloads, presigned GETs, copies and batch copies (their source), the
  check a restore makes, permanent deletes with their purge debt, and the
  trash's hard delete now resolve an object's bytes by its placement, and
  the bucket gate decides on that bucket's state. While no storage migration
  runs the placement is the collection's bucket, so nothing changes.
- After `BindCollectionToBucket` points a collection at another bucket, its
  existing objects keep being read from the bucket that holds them; until
  now they were looked for in the new bucket, which does not. New uploads go
  to the new bucket. The placement an authorised reader sees
  (`Object.placement`) is the bucket the bytes are in.
