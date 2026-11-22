## Unreleased — refusals no longer name the package that wrote them

- A call with no principal is refused `UNAUTHENTICATED` with `no
  authenticated principal`, and one whose principal has no tenant with `the
  principal is not bound to a tenant`; both used to start with `auth: `.
- A delegated capability refused for its user starts `delegated capability
  refused`, and a user gone is `the user a delegated capability acts for is
  gone`, where both started with `auth: `.
- `BillingService` on a deployment without the capability subsystem answers
  `UNAVAILABLE` with `billing handler not wired`, where it was `billing:
  handler not wired`.
- A batch the server could not check for want of its own wiring is
  `INTERNAL`, `the batch could not be submitted: the server is at fault`,
  where it was `UNAVAILABLE`, `batch: bucket lookup not wired` or `batch:
  object lookup not wired`; no deployment is wired so.
