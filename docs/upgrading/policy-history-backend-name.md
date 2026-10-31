## v41.4.0 — policy history names a backend by its name

- `PolicyService.ListPolicyVersions` reads `storageBackends/{backend}` and
  `storageBackends/{backend}/buckets/{bucket}` with the backend's name, as
  every other admin RPC does. It refused those names before, so a backend's
  and a bucket's history could not be read from the console; a backend's
  row uuid no longer resolves there.
