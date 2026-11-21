## Unreleased — a collection RPC the store fails answers INTERNAL

- `BindCollectionToBucket` to a bucket that is not registered answers
  `NOT_FOUND` (`ERROR_REASON_BUCKET_NOT_FOUND`), as `CreateCollection` does,
  and a store failure `INTERNAL`; both answered `FAILED_PRECONDITION`. A
  platform admin's rebind of another tenant's collection now applies.
- `GetCollection` answers a store failure `INTERNAL`, not `NOT_FOUND`;
  `ListCollections` answers it `INTERNAL`, not `UNKNOWN`.
- `EnsureTenantStorage` answers a collection whose bucket was deleted under
  it `NOT_FOUND`, not `INTERNAL`.
