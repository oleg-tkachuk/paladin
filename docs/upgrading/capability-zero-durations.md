## Unreleased — a zero capability duration means what it says

- `capability.verifier_leeway: 0s` now gives no clock skew on `nbf` and `exp`.
  Before, it gave limes's default of 30 seconds.
- `capability.revocation_cache_ttl` and `capability.dpop_nonce.lifetime` must
  now be positive, and the server refuses to boot on zero. Before, limes
  replaced a zero with its own default (2s and 5m) without saying so. The
  shipped configurations set both, so a deployment that keeps them is
  unaffected.
