# ADR-0012: Cedar knows the credential kind, and machines may delete their own objects

- **Status:** Accepted — implemented 2026-08-17 (`auth.PrincipalKind.String()`,
  `cedar.Principal.Kind` → `principal.kind`, built-in delete permit, call sites
  migrated to `apiutil.CedarPrincipal`, tests).
- **Related:** [ADR-0010](0010-capability-as-establishing-credential.md) (a
  capability establishes identity and carries no roles),
  [ADR-0011](0011-narrow-role-for-tenant-provisioning.md) (a narrow role for
  provisioning).

- **Context:** the per-tenant default policy permits `DeleteObject`,
  `RestoreObject`, `UpdateObject` and `CopyObject` only for a principal holding
  `objectKey:admin` or `platform.admin`. For a person that is correct — deletion
  is destructive and a tenant member should not do it casually.

  For the service that owns the object lifecycle it is unworkable, and there was
  no way around it. A consumer records an object, later removes the record, and
  has to remove the object with it; a garbage collector has to reap what nothing
  references. Neither can hold a role: an API token's principal carries none
  and a capability carries none **by
  design** (ADR-0010) — its authority lives in caveats, not roles. So every
  delete a consumer attempted came back `permission_denied`, under both
  credentials.

  The failure was silent, which is why it survived so long. A consumer's avatar
  replacement deleted the previous object best-effort and swallowed the denial
  into a warning, so each replacement orphaned a file; the sweeper written to
  reap those orphans shipped in dry-run and had never attempted a delete. It was
  found by trying one by hand against a live cluster.

  A role cannot express the distinction, because the credentials that need this
  cannot hold roles. What separates them from a tenant member is the kind of
  credential behind them — and Cedar could not see it: `cedar.Principal` carried
  subject, tenant, slug, roles and scopes, and `auth.PrincipalKind` stopped at
  the interceptor.

- **Decision:** two parts.

  1. **Cedar learns the credential kind.** `cedar.Principal` gains `Kind`, and
     the principal entity exposes `principal.kind` — `"user"`, `"api_key"`,
     `"service_account"`, `"capability"`, or empty. It is populated in
     `apiutil.CedarPrincipal` / `CedarPrincipalFor`, and the ~25 authorization
     sites that hand-built a `cedar.Principal` literal now go through those
     helpers. That migration is part of the decision, not tidying: a site that
     keeps its literal does not fail loudly, it silently stops matching any
     policy that reads the kind. The attribute is always present (empty when
     unknown) so a policy reading it cannot raise an evaluation error, which
     Cedar treats as deny.

  2. **A built-in permit for the delete family.** A principal whose kind is
     `api_key`, `service_account` or `capability` may `DeleteObject` and
     `RestoreObject` when the object is in **its own tenant**.

  The grant is bounded four ways: the credential must be a machine one (minted
  deliberately — a platform admin issues an API token, a capability-issuer
  issues a capability); the object must belong to that principal's own tenant; a
  capability is additionally confined by its own caveats, checked by the
  interceptor before Cedar runs; and the deletion is the soft kind — the row is
  marked, `RestoreObject` undoes it, and physical removal is a separate
  lifecycle path.

  `UpdateObject` and `CopyObject` are deliberately NOT included. They are not
  what a lifecycle owner needs, and widening the permit to "whatever a machine
  might want" is how a narrow grant stops being narrow.

  Built-in rather than per-tenant, for the reason `EnsureTenantStorage` is:
  a tenant's stored policy is frozen at creation, so a template change reaches
  no existing tenant. A tenant that wants its machines held to the role can
  `forbid` the actions for a kind explicitly; first-forbid wins.

- **Consequences:**
  - A consumer can finally clean up after itself: deletions that were silently
    denied now succeed, and a garbage collector can come out of dry-run.
  - Policy authors gain a genuinely useful primitive. "Machines may X, people
    may not" was previously inexpressible, and role-shaped workarounds do not
    reach capability principals at all.
  - A machine credential's blast radius grows by soft-deletion within its own
    tenant. It could already overwrite those objects (`PutObject` in the default
    template), and soft deletion is recoverable, so the marginal risk is small —
    but it is not zero, and a tenant that disagrees has an explicit `forbid`.
  - `SimulateAuthz` cannot express a kind, so it will not reproduce decisions
    from kind-gated policies. Noted at the call site; worth adding to the RPC
    when somebody needs it.
