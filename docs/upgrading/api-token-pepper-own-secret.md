## Unreleased — the API-token pepper is a secret of its own

- With `api_token.enabled`, either `api_token.hmac_key` or
  `api_token.hmac_key_secret` is now required, and the config is refused at
  load without one. The pepper is no longer derived from `auth.signing_key`
  (ADR-0047).
- To keep the API tokens already issued, set the pepper to the value the
  server derived: HMAC-SHA256 keyed by `auth.signing_key`, over the bytes
  `paladin-api-token-hmac-v1`, stored raw. Any other value makes every issued
  API token stop verifying.
- The chart generates `<release>-api-token-hmac` when API tokens are on and
  the config names no pepper, as it does the signing key; under ArgoCD set
  `apiToken.hmacKey.existingSecret` or `config.api_token.hmac_key_secret`.
- Only the roles that mint or verify sessions need `auth.signing_key` now: a
  role started without it fails when it builds a session verifier, not in
  every role at startup.
