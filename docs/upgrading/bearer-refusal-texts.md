## sdk/python/v0.151.1, sdk/go/v0.151.1 — a refused bearer token is answered without the JWT library's words

Codes are unchanged; only the message text moved. A client that matches on
the text should match on the code and, for a capability, the reason instead.

- A bearer token the planes or the IAM plane refuse is answered with the
  reason alone. `jwt: malformed token: jws.ParseString: failed to parse
  string: …` is `malformed bearer token`; `jwt: token expired: …` is `bearer
  token expired`, and likewise `bearer token not yet valid`, `bearer token
  issuer mismatch`, `bearer token audience mismatch`, `bearer token signature
  invalid`, `bearer token signed by an unknown key` and `bearer token claims
  invalid`. Any other failure to verify one, such as a key set that would not
  load, is `bearer token could not be verified`. The library's text stays in
  the server's log.
- `AuthService.RefreshToken` and `ExchangeAudience` refuse a refresh token
  the same way, naming it: `refresh token: jwt: malformed token: …` is
  `malformed refresh token`, and likewise `refresh token expired`, `refresh
  token audience mismatch` (`refresh token: wrong audience` before) and
  `refresh token claims invalid` (a token without `refresh: true`, a jti or a
  user_id).
- `paladintest` and `paladin.testing` under strict auth answer a bearer token
  they did not issue `malformed bearer token`, and a revoked one `bearer token
  expired`.
- Both fakes answer a revoked capability as the server does: `The access
  token is invalid`, with reason `CAPABILITY_REVOKED` and limes code
  `revoked`, rather than `capability: revoked` with no reason. `paladintest`
  answers a capability it does not know the same way with reason
  `CAPABILITY_INVALID_TOKEN`, rather than `capability: invalid signature`.
