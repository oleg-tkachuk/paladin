## Unreleased — the HTTP edges answer a key set that will not load with 503

- The MCP server's `/mcp`, `/sessions` and `/status` endpoints and the audit
  stream answer a bearer token whose signing key could not be fetched with
  `503 Service Unavailable` and the body `the keys that verify a bearer
  token could not be loaded; retry the call`, as the RPCs answer it
  `UNAVAILABLE`. They used to answer `401`, which told an OAuth client to
  re-authorize a token that may be good. The 503 carries no
  `WWW-Authenticate` challenge: retry the request instead.
- A token refused — expired, forged, another audience — is still `401` with
  the challenge it had.
