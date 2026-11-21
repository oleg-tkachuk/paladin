## Unreleased — UserService tells a failing store from a missing user

- Every `UserService` RPC that reads a user first (`UpdateUser`,
  `DeleteUser`, `GrantScopes`, `RevokeScopes`, `ResetPassword`,
  `ListOAuthGrants`, `RevokeOAuthGrant`, `ListUserCapabilities`) answers
  `INTERNAL` when the store fails. It used to answer `NOT_FOUND`, as if the
  user were gone.
- `GrantScopes` and `RevokeScopes` answer `NOT_FOUND` for a user removed
  meanwhile and `ABORTED` for one changed meanwhile, instead of `INTERNAL`.
- `ResetPassword` on a user removed meanwhile answers `NOT_FOUND` instead of
  `INTERNAL`.
- `ListUsers` answers `INVALID_ARGUMENT` for a `page_token` it did not mint
  and `INTERNAL` for a failing store; both used to answer `UNKNOWN`.
- `CreateUser` and `ResetPassword` answer `INTERNAL` when the password cannot
  be hashed for a reason other than its length; a password longer than 72
  bytes is still `INVALID_ARGUMENT`.
