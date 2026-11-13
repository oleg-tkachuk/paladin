## Unreleased — limes v1.0.0-rc.71: refusal codes and stricter token-endpoint input

- A capability whose upstream names no revocation lineage (limes code
  `no_revocation_lineage`) is answered `INTERNAL`, where it was
  `UNAVAILABLE`: it is a configuration to fix, and a client that retried on
  `UNAVAILABLE` should not retry it.
- The MCP token endpoint (`/oauth/token`) reads `scope` as RFC 6749 §3.3
  writes it: printable ASCII tokens separated by single spaces. A tab, a
  line break, a quote, a backslash or a non-ASCII byte in it is refused
  with `invalid_scope` (400), where it was split on any whitespace.
- Introspection answers `active: false` for a token of an audience the
  asking client does not serve, before its revocation is looked up; it
  could answer `server_error` when the revocation store failed.
- The key set the exchange fetches for subject tokens is capped at 1 MiB; a
  larger document leaves the keys unavailable until a smaller one is served.
- No migrations, and no configuration keys change.
