## Unreleased — metering errors answer with what a retry can mend

- A metering error that is the server's own fault — a misconfiguration, a
  call scoped to the wrong tenant, a broken invariant — is now `INTERNAL`,
  not `UNAVAILABLE`, so clients stop retrying it.
- A refusal of the capability met while metering is `PERMISSION_DENIED`, as
  the same refusal is when the capability is verified, and a record that is
  already there is `ALREADY_EXISTS`.
- `UNAVAILABLE` is left for a failing dependency — the store, the keys, the
  revocation upstream — which a retry may find recovered.
