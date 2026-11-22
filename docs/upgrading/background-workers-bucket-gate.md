## Unreleased — background deletes wait for a backend that takes writes

- The lifecycle hard delete and the purge drainer no longer delete bytes on a
  draining (read-only) or disabled backend, as `DeleteObject` with `permanent`
  is refused there. A trashed object past its retention stays in the trash,
  restorable, and a purge debt stays in `pending_purges`, unchanged, until
  the backend takes writes again; the first sweep after that removes them.
  Each sweep logs every bucket it defers at Warn, with the gate's reason.
- A tenant purged while its bytes sit on a draining backend keeps those bytes
  until the drain ends. A backend disabled for good rather than returned to
  service keeps its purge debts on record, and its buckets cannot be deleted
  while they are there.
- The multipart reaper and the abort drainer no longer abort uploads on a
  disabled backend; the sessions and abort debts stay until it is enabled
  again. A draining backend still takes the abort: parts were never an
  object, and aborting them frees storage.
- The reconciler no longer deletes the bytes of an upload that broke its
  registration on a draining or disabled backend; the object stays `PENDING`
  until the backend takes writes again, and each tick logs the deferral at
  Warn.
