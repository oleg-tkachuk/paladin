## Unreleased — writing an event subscription that is not there is NOT_FOUND

- `UpdateEventSubscription` and `DeleteEventSubscription` on a subscription
  removed after it was read now answer `NOT_FOUND` with
  `ERROR_REASON_NOT_FOUND`. They used to answer `ABORTED` with
  `ERROR_REASON_VERSION_CONFLICT`, which told a client to re-read and retry
  what can never succeed. `ABORTED` is still the answer for a stale
  `resource_version`.
- `ListEventSubscriptions` answers `INTERNAL` when the store fails; it used
  to answer `UNKNOWN`.
