## Unreleased — the capability signing key can be rotated

- `capability.previous_public_keys` lists keys rotated out of signing, each a
  PKIX PEM public key at `path` with an optional `kid`. The planes keep
  verifying the capabilities they signed, and the capability JWKS keeps
  publishing them; nothing signs with them. Empty by default, so nothing
  changes until it is set.
- The chart mounts the Secret keys named in
  `capabilitySigningKeySecret.previousKeys` beside the signing key.
- The procedure is in
  [the rotation runbook](../runbooks/capability-signing-key-rotation.md).
