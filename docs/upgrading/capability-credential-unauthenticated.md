## sdk/go/v0.137.0, sdk/python/v0.138.0 — a capability that is no longer good is UNAUTHENTICATED

- A capability the server cannot accept as a credential — not issued by it,
  malformed, expired or not yet valid, revoked, past its delegation depth,
  presented without its proof or under the wrong scheme, or to a plane its
  audience does not name, and a Biscuit copy that widens its capability — is
  refused `UNAUTHENTICATED`, was `PERMISSION_DENIED`. The reason in the
  `ErrorInfo` is unchanged. A client that treated `PERMISSION_DENIED` as
  "replace the capability" checks `UNAUTHENTICATED`, or the reason.
- A capability that is good and does not allow the call — an operation,
  resource or idempotency key its caveats refuse, a delegation wider than its
  parent — stays `PERMISSION_DENIED`. So does an operation on a record that is
  revoked or expired, such as leasing for a revoked child.
- The SDK fakes (`paladintest`, `paladin.testing`) answer the same, and the MCP
  bridge tells a model why a capability was refused.
