## release/v0.2.0, sdk/python/v0.145.1, sdk/go/v0.145.1 — the SDKs stop retrying a permanent refusal

- Both SDKs' default retry no longer repeats a `ResourceExhausted` whose
  limes code the capability library holds permanent — `request_limit_exceeded`,
  `copy_limit_exceeded`, `lease_requests_exhausted`. Such a refusal stands for
  as long as the capability does: get another capability instead.
- `sdk/testdata/error_info.json` lists every permanent code, held to limes's
  `Reason.Permanent` by the Go SDK's tests; the Python SDK exposes the same
  set as `paladin.errors.PERMANENT_LIMES_CODES`.
