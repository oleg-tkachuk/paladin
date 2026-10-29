## v41.2.0 — verifying and metering a capability answer alike

- A spent request or copy limit refused while verifying a capability is
  `RESOURCE_EXHAUSTED`, not `PERMISSION_DENIED`, as it already was while
  metering.
- The server's own fault while verifying a capability is `INTERNAL`, not
  `UNAVAILABLE`, and is no longer worth retrying.
- A server-side metering failure no longer tells the caller the store's
  error text; the cause is logged.
