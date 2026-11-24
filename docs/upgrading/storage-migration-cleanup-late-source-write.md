## Unreleased — a storage migration's cleanup keeps a source write no event has recorded

- Migration 120 adds the `storage_migration_source_etags` table: the ETag
  the source bucket reported for each object key just before the migration
  copied it. The copy and the catch-up now read it with one extra HEAD per
  object.
- The cleanup of a completed migration no longer deletes a source copy
  whose ETag differs from the one the copy read, for a live object whose
  last recorded write predates the rebind: that is an overwrite whose
  storage event has not arrived. It is kept and logged at Warn ("cleanup:
  the source holds a write the copy did not take …"), and the migration
  stays `completed` until the event moves the write into the dedicated
  bucket; the next pass then deletes the source copy.
- If the event was dropped, the migration stays `completed` and the line
  repeats on every pass, naming the key.
- A migration that copied before this release has no recorded ETags. Its
  cleanup behaves as before.
