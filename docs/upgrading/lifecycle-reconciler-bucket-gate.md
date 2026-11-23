## Unreleased — lifecycle expiry and the reconciler skip a disabled backend

- The lifecycle expiry no longer moves objects to the trash on a disabled
  backend, as `DeleteObject` to the trash is refused there. Its buckets are
  skipped each tick, logged at Warn with the gate's reason, and expire on the
  first tick after the backend is enabled. A draining backend still expires.
- The reconciler no longer HEADs, promotes or discards pending uploads on a
  disabled backend; they stay `PENDING`, and the overdue-pending gauge counts
  them, until it is enabled. Each tick logs the deferral at Warn.
