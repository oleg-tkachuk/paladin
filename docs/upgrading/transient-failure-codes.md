## Unreleased — transient failures answered with their own codes

- An RPC that fails because the database cannot be reached, or is shutting
  down or starting, now answers `UNAVAILABLE` with a generic message instead
  of `INTERNAL`. A client may retry it. The driver's error stays in the
  server's failure log as the cause.
- An RPC whose caller cancelled it, or whose deadline passed, now answers
  `CANCELED` or `DEADLINE_EXCEEDED` instead of `INTERNAL`, and is logged at
  info rather than error.
- A handler that chose a precise code keeps it; only failures reported as
  `INTERNAL`, `UNKNOWN` or `DATA_LOSS` are reclassified.
- An object, presign or multipart RPC whose collection lookup fails used to
  answer `NOT_FOUND` with the driver's error, the database's address
  included. It now answers `NOT_FOUND` only for a collection that is not
  there; a failed lookup answers `UNAVAILABLE` or `INTERNAL` like any other.
