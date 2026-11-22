## Unreleased — a key set that will not load is answered Unavailable

- A bearer or refresh token whose signing key the server must fetch, from an
  identity provider that does not answer, is answered `UNAVAILABLE` rather
  than `UNAUTHENTICATED`: the server could not decide, and the token may be
  good. The message is `the keys that verify a bearer token could not be
  loaded; retry the call` (`… a refresh token …` on the IAM plane's refresh),
  where it was `bearer token could not be verified`. Both SDKs already retry
  `UNAVAILABLE`; a client that treated this answer as a bad token and asked
  for a new one should retry instead.
- A token signed by a key the identity provider does not publish, after a
  fetch that succeeded, is still `UNAUTHENTICATED`, `bearer token signed by
  an unknown key`.
