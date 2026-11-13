## release/v0.3.0 — sessions are signed ES256 by a key only the minting role holds

- Access and refresh tokens, and the OAuth AS's, are signed ES256 by the key
  at `auth.session_key_path` (ECDSA P-256, PEM PKCS#8 or SEC1) and carry its
  RFC 7638 thumbprint as `kid` (ADR-0047). HS256 is refused everywhere, so
  every session issued before the upgrade ends once and users sign in again.
- `auth.signing_key` and `auth.signing_key_secret` are removed; a config that
  sets either is refused at load. The chart no longer generates
  `<fullname>-auth-signing-key` and ignores `auth.signingKey`, which goes in a
  later release.
- `serve api` serves the session JWKS at `/.well-known/jwks.json` on the IAM
  listener. Every other role verifies by it: set `auth.jwks_url` to that
  endpoint, and `auth.jwks_ca_path` when it is served under a private CA. The
  chart sets both, and `session_key_path` to the key `auth.sessionKey` mounts.
- To rotate, put the new key at `auth.session_key_path` and the old public key
  in `auth.previous_session_public_key_paths` until the refresh-token
  lifetime has passed.
- `task backend:auth:mint-token` signs a dev session with
  `JWT_ALG=ES256 JWT_SECRET="$(cat <key.pem>)" STEP_FLAGS="--kid <kid>"`,
  the kid from the JWKS.
