## sdk/go/v0.127.0, sdk/python/v0.128.0 — a capability shows the OAuth grant it was exchanged under

- `Capability` carries `session` (the OAuth grant's id), `on_behalf_of` (the
  user the subject acts for) and `client_id`, output only; `List` and `Get`
  set `session`.
- The console's capability list and detail link a delegated capability to
  its grant on the user's page.
