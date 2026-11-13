## release/v0.3.0, sdk/python/v0.146.3, sdk/go/v0.146.3 — limes v1.0.0-rc.73: a bumps table in the limes schema

- pgstore migration 044 adds `limes.bumps`. The `migrate` hook applies it
  before the new pods serve; a pod of this release refuses to start against
  a limes schema still at version 43.
- A refusal is answered with the problem type, status and challenge of the
  limes code it names, where an error carrying one refusal's code inside
  another's family took the family's.
- No configuration keys change.
