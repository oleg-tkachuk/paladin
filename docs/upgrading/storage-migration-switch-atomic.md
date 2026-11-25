## Unreleased — a storage migration's switch is one transaction

- A storage migration points the tenant's collections, default binding and
  layout at the dedicated bucket in the same transaction that moves it to
  `moving`, and back at the shared bucket in the one that moves it to
  `rolling_back`, the worker's rollback and a cancel alike. An upload made
  right after the switch is signed for the new bucket; it used to be signed
  for the old one until the next pass, up to a worker interval later.
- A collection the schema refuses to move now fails a migration still in
  `provisioning`, with nothing pointed at the dedicated bucket.
- Each repoint is logged at INFO with the tenant, both buckets and the
  collections repointed.
