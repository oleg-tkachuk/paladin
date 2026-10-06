# ADR-0011: A narrow role for tenant provisioning

- **Status:** Accepted — implemented 2026-08-17 (`apiutil.RoleTenantProvisioner`,
  built-in Cedar permit, gates on `CreateTenant` / cross-tenant `GetTenant` /
  policy-only `UpdateTenant` / cross-tenant object-key create+get, tests).
  Same shape as `platform.capability-issuer`
  ([ADR-0024](0024-credential-actions-and-the-capability-issuer-grant.md)).

- **Context:** a consumer that creates accounts needs the matching Paladin tenant to
  exist before its first upload, and it cannot wait for an operator to run a
  command. Provisioning takes four things: create the tenant, create (or adopt)
  its bucket, create its object keys, and set its inherited policy so the
  consumer's own credential is admitted.

  Until now the only credential that could do any of that was `platform.admin`,
  which the built-in policy grants **unconditionally on every action and every
  resource**. Handing that to a long-running provisioning job means a credential
  sitting in a cluster that can delete any tenant, purge its objects, read any
  document, and mint further credentials. The blast radius of its leak is every
  tenant's data — for a job whose entire purpose is to *add* rows.

  Capability issuing faced this and answered it with a role named for exactly
  one job: `platform.capability-issuer` may mint a capability for any tenant and
  do nothing else (its built-in permit is
  [ADR-0024](0024-credential-actions-and-the-capability-issuer-grant.md)).
  Provisioning is the same problem one step earlier.

- **Decision:** add `platform.tenant-provisioner`. It may create a tenant, its
  bucket, its object keys (and bind them), read those resources, and set a
  tenant's inherited policy — for **any** tenant. It may do nothing else.

  Two mechanisms, both required, because either alone would be a hole:

  1. **A built-in Cedar permit** listing the exact actions: `ManageTenant`,
     `ReadTenant`, `ManageBucket`, `ReadBucket`, `ManageObjectKey`,
     `BindObjectKeyToBucket`. Built-in rather than per-tenant, because a tenant's
     stored policy is written *by* provisioning — a permit that lived in the
     tenant's own policy could never authorise the call that creates it.
  2. **Handler gates** that admit the role only on the provisioning RPCs.
     `ManageTenant` is the action behind delete, purge, restore, rename and
     storage migration as well as create, so the Cedar permit alone would grant
     tenant destruction. Those handlers keep `requirePlatformAdmin`; only
     `CreateTenant`, the cross-tenant branch of `GetTenant`, and `UpdateTenant`
     restricted to a policy-only change accept the new role.

  `UpdateTenant` is gated on the **shape** of the update, not on the RPC that
  produced it: `SetInheritedPolicy` routes through `UpdateTenant`, which can also
  rewrite display name and labels. A provisioner update carrying anything beyond
  the policy is refused outright rather than having the extra field silently
  dropped — a silent drop would make the credential's real authority differ from
  its documented one, which is how a "narrow" role stops being narrow.

  The role has **no data-plane reach at all**: no `GetObject`, `PutObject`,
  `DeleteObject`, `PresignGet`/`PresignPut`. A provisioner can bring a tenant's
  storage into existence and cannot read or write one byte in it. It also cannot
  create or rotate a storage backend (`ManageBackend`), and it cannot mint a
  credential — granting roles at token creation is `platform.admin`-only, so a
  provisioner cannot promote itself or issue a token that outlives it.

- **Consequences:**
  - A consumer can provision automatically with a credential whose leak adds
    tenants and buckets — noisy and reversible — instead of one that deletes
    everything.
  - A tenant can still refuse it: the built-in is a `permit`, and Cedar's
    first-forbid wins, so a tenant policy may `forbid` the role explicitly.
  - Two places now decide this role's authority (the built-in permit and the
    handler gates), and they must not drift. The gates are the stricter of the
    two by construction: relaxing the permit cannot reach a
    `requirePlatformAdmin` RPC, and the tests assert both directions —
    the permit's action list, and that delete/purge/restore/rename/migrate
    remain admin-only.
  - `platform.admin` keeps working for everything, unchanged.
