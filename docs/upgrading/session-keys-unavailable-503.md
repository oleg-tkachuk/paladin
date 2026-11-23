## Unreleased — the session role answers a key set that will not load as 503

- `POST /session/refresh` answers `503` with `{"code":"unavailable"}` and the
  message `the keys that verify a bearer token could not be loaded; retry the
  call` when the keys that verify the session's refresh token cannot be
  loaded; it used to answer `204`. The session cookie is kept. A refresh
  token IAM refuses is still `401`.
- The console keeps the session on that answer, shows that sign-in is
  temporarily unavailable and checks again with backoff. An older console
  reads the `503` as signed out, so deploy the console with or before the
  session role.
