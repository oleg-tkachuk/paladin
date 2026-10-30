## v41.3.0, sdk/go/v0.127.0 — limes v1.0.0-rc.24

- pgstore v0.52.1 adds four migrations, applied at startup:
  - `027` rebuilds the client and session indexes to span tenants
    (`CREATE INDEX CONCURRENTLY`);
  - `028` replaces the revocation notify trigger, sending a large
    revocation in parts;
  - `029` adds `limes.capabilities.run_id` and backfills it from each
    capability's agent principal — a full pass over the table;
  - `030` indexes it (`CREATE INDEX CONCURRENTLY`).
