## v41.4.0, sdk/go/v0.128.0, sdk/python/v0.129.0 — the capability list takes an OAuth session

- `CapabilityService.List` takes `session`: the capabilities exchanged under
  that OAuth session, a grant's id — what revoking the grant revokes. The
  fakes in `paladintest` and `paladin.testing` filter on it too.
- The console's grant panel on a user's page links to those capabilities;
  the capability list reads the session from `?session=`.
