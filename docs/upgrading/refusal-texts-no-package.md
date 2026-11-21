## sdk/python/v0.151.0, sdk/go/v0.151.0 — refusal messages no longer begin with a package name

Codes and `ErrorInfo` reasons are unchanged; only the message text moved. A
client that matches on the text should match on the reason instead.

- API tokens: `api_token: token not found` is `API token not found`, and the
  same for `malformed API token`, `API token expired`, `API token revoked`,
  `API token audience mismatch`, `invalid API token request` and the rate-limit
  refusals (`API token rate limit …`). The Go and Python SDK fakes answer the
  same.
- IAM: `auth/store: resource_version mismatch` is `resource_version mismatch`,
  and `auth/store: subject already exists in tenant` is `subject already exists
  in tenant`; `invalid page_token` and `resource not found` likewise.
- A batch's per-object reason `statemachine: version conflict` is `object
  version conflict`, and `statemachine: object not found or wrong state` is
  `object not found or not in the expected state`.
- `cedar: stored policy does not compile` is `stored policy does not compile`.
