## v41.0.0, sdk/go/v0.126.0, sdk/python/v0.127.0 — limes v1.0.0-rc.11

- **Every capability is reissued.** limes issues one token format, an RFC
  9068 access token (`typ: at+jwt`), and refuses the earlier
  `limes-cap-v6+jwt` tokens and their Biscuits. Capabilities issued before
  the upgrade stop working; issue them again.
- **`capability.signing_key_kid` names a `limes:` kid.** limes signs and
  verifies only under a kid in its namespace; a set kid without the prefix
  stops the server at startup. The kid derived when it is empty already has
  it.
- **A capability names its OAuth client** (RFC 9068 `client_id`): the
  client the issuer acts for, or `paladin` when it acts for none.
- **Migrations of limes's own schema** (pgstore v0.42) run with the server's;
  nothing to do.
