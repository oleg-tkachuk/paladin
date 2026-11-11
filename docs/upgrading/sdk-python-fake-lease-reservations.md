## sdk/python/v0.145.6, sdk/go/v0.145.6 — the Python SDK's fake holds a lease against the chain's ceilings

- Under `strict_auth=True`, the requests a lease holds are reserved against
  the capability's request ceiling and each ancestor's, as the server and
  the Go fake hold them. A lease past them is refused `RESOURCE_EXHAUSTED`
  with limes code `request_limit_exceeded`, and a request elsewhere on the
  chain finds the reserved requests already taken.
- Settling a lease counts the requests made under it on the ancestors.
  Before, they were counted on the leaseholder and below it only.
- A top-up of an expired lease no longer carries what that lease had left.
