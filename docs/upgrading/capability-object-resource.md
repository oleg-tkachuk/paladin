## sdk/python/v0.151.5, sdk/go/v0.151.5 — a capability names a nested collection as one segment

- The resource a capability names an object by is
  `object://{tenant-id}/{collection segment}/{key}`: a nested collection's
  `/` is written `.`, as in the bucket path. Collection `invoices/2026` with
  key `r.pdf` is `object://{tenant-id}/invoices.2026/r.pdf`. Collections
  without a `/` keep their resources.
- A resource prefix on a collection now covers that collection's objects
  only. It used to cover every collection nested under it too, and a
  collection `a/b` with key `c` shared its resource with collection `a` and
  key `b/c`.
- `paladin.ObjectResource` (Go) and `paladin.object_resource` (Python) build
  the new form, and both SDKs' fakes match on it; `sdk/testdata/object_resources.json`
  holds the server and both SDKs to the same resources and coverage.
- Capabilities issued before this release keep their resources as written,
  and the server reads them in the new form. A resource that named a nested
  collection, such as `object://{tenant-id}/a/b/`, now covers the keys under
  `b/` of collection `a` and none of collection `a/b`'s objects: for a
  resource written in the old form, every object it reaches now it reached
  before. The exception is a resource with a `.` where the collection goes
  (`object://{tenant-id}/a.b/`): it matched no object before and now covers
  collection `a/b`. Revoke capabilities and delegations scoped to a nested
  collection, or holding such a resource, and issue them again with the SDK
  helpers; where the data is disposable, revoke them all.
- An SDK older than this release still builds the old form for a nested
  collection, which the server reads as described above. Upgrade the SDK
  with the server.
