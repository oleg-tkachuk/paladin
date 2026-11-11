## sdk/python/v0.145.4, sdk/go/v0.145.4 — the SDK fakes spend a request from its lease

- Under `WithStrictAuth` / `strict_auth=True`, a request made under a
  capability's lease of requests, or under an ancestor's, is now spent from
  the lease, as the server spends it: `GetLease` reports it in
  `requests_spent`. A request past what the lease holds is refused
  `RESOURCE_EXHAUSTED` with limes code `lease_requests_exhausted`. Before,
  the fakes counted such a request against the request ceilings and never
  against the lease.
- A top-up holds the requests the open lease had not yet spent, plus the
  top-up, no longer all the requests it was opened with.
- The Go fake now counts and leases through limes's `memstore.UsageStore`.
  So a lease's requests are also reserved against the chain's request
  ceilings, and a lease past them is refused, as on the server.
