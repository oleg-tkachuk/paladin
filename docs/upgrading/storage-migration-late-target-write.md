## Unreleased — a late target write after a storage migration's rollback is moved

- Migration 119 adds `tenant_storage_migrations.rebound_back_at`: when a
  rolled-back migration rebound the tenant's collections back to the source.
  The rebind back sets it; a retry clears it.
- A storage event for an overwrite in a rolled-back migration's dedicated
  bucket that is delivered after the rebind back is no longer skipped: the
  ingest copies the object's bytes into the source bucket and promotes them
  there, logged as "late source write moved into the collection's bucket".
  It is skipped, logged at Info, when the event's time is before the
  migration's rebind or missing (the migration's own copies raise such
  events), or when the object was written since the rebind back.
