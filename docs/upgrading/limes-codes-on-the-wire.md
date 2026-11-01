## Unreleased — the limes code of every capability error

- Every error that comes from the capability library carries its limes code
  — `budget_exceeded`, `revoked`, `not_found`, `store_unavailable`, … — in
  the `google.rpc.ErrorInfo` of domain `paladin`, under the metadata key
  `limes_code`, beside the reason a refusal already carried. An error with a
  code and no reason now has an `ErrorInfo` with an empty reason.
- The Go SDK reads it as `Error.LimesCode` and `paladin.LimesCode(err)`; the
  Python SDK as `PaladinError.limes_code` and `paladin.limes_code(err)`.
  Both SDKs' fakes send it as the server does.
