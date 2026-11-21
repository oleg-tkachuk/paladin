## Unreleased — a quota for a tenant or bucket that is not there is NOT_FOUND

- `SetQuota` for a tenant, or a backend and bucket, that does not exist now
  answers `NOT_FOUND`. It used to answer `INTERNAL` with the database's
  constraint error as its message. A client that retried an `INTERNAL` here
  should stop and check the tenant or bucket it names.
