## Unreleased — fixed texts for OAuth client refusals and unbuildable messages

- An RPC whose request or response message the server cannot build is
  `INTERNAL` with the message `the server cannot build this procedure's
  messages`. It used to carry the procedure's name and the type registry's own
  error. This is a build fault, unreachable with generated stubs; the cause is
  logged at Error.
- The `invalid_client` answers of `/oauth/token` and `/oauth/revoke` keep their
  `error_description` texts (`unknown client`, `client_secret required`,
  `invalid client_secret`), now fixed sentences rather than an error's text.
  The refusal's log line carries the cause in a `cause` field.
