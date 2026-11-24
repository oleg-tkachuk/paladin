## Unreleased — every object row is placed or anchored

- Migrations 132 and 133 make an object's placement a schema guarantee
  (ADR-0051): a `PENDING` row must name the bucket its upload URL is signed
  for, and any other row the bucket its bytes are in. Rows an older binary
  left without them are placed in, or anchored to, their collection's bucket
  first. Upgrade from a release that already records placement (migration
  123 onward): a binary from before it, still serving while the migration
  runs, would have its promotes refused.
- `DeleteBucket` now refuses, with `FAILED_PRECONDITION` naming objects, a
  bucket an upload is still anchored to. It used to delete the bucket and
  clear the anchor.
