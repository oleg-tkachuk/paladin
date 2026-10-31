## sdk/go/v0.130.0, sdk/python/v0.131.0 — capabilities acting for a user

- `UserService.ListUserCapabilities` (IAM) lists the capabilities acting on
  a user's behalf: those an agent exchanged under the user's OAuth grants,
  and those delegated from them, newest first; `grant_id` narrows them to
  one grant's. A user may list their own,
  without the admin audience; a tenant admin those of the tenant's users.
- The new Cedar action `ReadUserCapabilities` has the same built-in permits
  as `ReadOAuthGrants`. A tenant policy that forbids `ReadOAuthGrants` should
  forbid it too.
