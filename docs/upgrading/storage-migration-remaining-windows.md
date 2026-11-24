## Unreleased — the remaining storage migration windows are closed

- A storage event for an overwrite in a migration's source bucket that is
  delivered after the migration rebound the collection is no longer skipped:
  the ingest copies the object's bytes into the collection's new bucket and
  promotes them there, logged as "late source write moved into the
  collection's bucket". It is skipped, logged at Info, when the object was
  written in the new bucket since the rebind. An event delivered after the
  migration's cleanup deleted the source copy is logged at Error: those bytes
  are gone. The `serve ingest` role now copies objects between buckets, with
  the storage credentials it already uses for its HEADs.
- Every promote now holds a transaction-scoped advisory lock for its tenant,
  shared; starting or retrying a storage migration, and handing a stuck one
  to the rollback, takes it exclusively, so the start waits for the
  tenant's promotes in flight.
- `CreateCollection` for a private collection answers `ABORTED` ("a storage
  migration moved the tenant's collections away from this bucket; retry")
  when it names the bucket the tenant's storage migration has just moved its
  collections away from: the source between the rebind and the cleanup, or
  the dedicated bucket after a rollback. A retry that names no bucket
  resolves the tenant's new default binding. A creation racing the rebind
  is now moved with the tenant's other collections.
