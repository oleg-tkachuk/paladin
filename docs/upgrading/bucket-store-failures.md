## Unreleased — a bucket RPC the store fails answers INTERNAL

- `ListBuckets` and `ListAccessibleBuckets` answer a store failure as
  `INTERNAL`; they used to return it with no code, which clients saw as
  `UNKNOWN`.
- `SetBucketPolicy`, `SetLifecycleRules`, `SetObjectLock`, `SetVersioning`
  and `SetReplication` answer `INTERNAL` when the bucket cannot be read back
  after the write. They used to answer success with an empty bucket.
