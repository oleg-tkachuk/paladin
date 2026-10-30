## Unreleased — revoking a connected application stops its capabilities

- Revoking an OAuth grant from the console (`UserService.RevokeOAuthGrant`)
  now also revokes every capability exchanged under the grant's session, as
  the client's own `/oauth/revoke` already did.
- Revoking a grant that is already revoked succeeds instead of answering
  `NOT_FOUND`, so a call that failed part-way is finished by a retry. A grant
  that is not the user's is still `NOT_FOUND`.
- If the capabilities could not be revoked, the call answers `UNAVAILABLE`;
  retry it.
