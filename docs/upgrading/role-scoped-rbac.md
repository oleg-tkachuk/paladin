## Unreleased — each role's service account reads only its own Secrets

- The chart renders one Role per role, `<release>-secret-reader-<role>`,
  naming the Secrets that role's config makes it read (ADR-0047), in place of
  the shared `<release>-secret-reader`. A role that reads none gets no Role.
- `rbac.secretReader.secretNames` is now granted to every role on top of its
  own; every config ref is derived, so drop entries that only repeated one.
- Secrets named by data — a registered storage backend's
  `credentials_secret_ref`, an event sink's `k8s:` refs — must live in
  `config.runtime_secrets.namespace`, which you create; the admin plane, the
  dispatcher and bootstrap may read any Secret there, and a ref naming another
  namespace is refused. Left empty, no such Secret is readable.
