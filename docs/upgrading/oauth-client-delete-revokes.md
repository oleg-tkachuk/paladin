## v41.3.0 — deleting an OAuth client revokes its capabilities

- `OAuthClientService.DeleteOAuthClient` now revokes every capability
  exchanged under the client, in every tenant, and releases their holds.
  Before, they stayed usable until their tokens expired.
- Deleting a client already gone still runs the revocation before answering
  `NOT_FOUND`, so a retry finishes a delete whose revocation failed; that
  failure answers `UNAVAILABLE`.
- Revoking a grant's session, from the console or by the client, releases
  the revoked capabilities' holds at once instead of when they expire.
