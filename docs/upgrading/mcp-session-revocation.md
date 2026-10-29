## Unreleased — ending an OAuth grant revokes the capabilities it was exchanged for

- **Migration time.** pgstore adds `client_id`, `on_behalf_of` and
  `session` columns to `limes.capabilities`, fills the first two from every
  existing record in one `UPDATE`, and builds four indexes `CONCURRENTLY`. On
  a large table the migration takes longer, and the `UPDATE` holds the rows
  it rewrites while it runs.
- **OAuth access tokens carry `sid`**, the grant's refresh-token family.
  Revoking a grant (`/oauth/revoke`), or a replayed refresh token revoking
  its family, now revokes the capabilities the grant's tokens were exchanged
  for at once.
- Capabilities issued before the upgrade record no session, so ending a
  session does not reach them; they expire with their token as before.
