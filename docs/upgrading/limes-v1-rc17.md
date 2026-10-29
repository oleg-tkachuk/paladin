## Unreleased — limes v1.0.0-rc.17: PostgreSQL 15 and pgstore's DPoP replay cache

- **PostgreSQL 15 or later.** pgstore v0.45 reads capability records through
  a `security_invoker` view, which an earlier PostgreSQL cannot create; the
  migration fails there.
- **DPoP proof ids move to `limes.dpop_seen_proofs`.** pgstore keeps the
  replay cache now, and migration `104` drops Paladin's `dpop_seen_proofs`.
  The new table starts empty: a proof accepted in the minute before the
  upgrade could be presented once more in that minute. Nothing to do.
