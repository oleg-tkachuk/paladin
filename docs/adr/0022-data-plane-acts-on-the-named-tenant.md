# ADR-0022: The data plane acts on the tenant a platform admin names

- **Status:** Accepted 2026-10-04.

- **Context.** ADR-0016 keeps a platform admin's reach across tenants: Cedar
  grants it by role, and the data plane's tenant check lets a platform admin
  name any tenant. But the data plane then dropped the tenant it had checked
  and acted on the admin's own: a listing of another tenant's collection
  showed the admin's same-named collection, or nothing, and a write would have
  landed there too. The console made it worse by naming the signed-in user's
  tenant on every tenant's pages. The admin plane does this right: it scopes
  the request to the target tenant (`auth.WithActingTenant`) after its role
  and Cedar checks, so RLS reads and writes that tenant's rows.

- **Decision.**
  - **A request acts on the tenant its names name.** The data plane's name
    parsing returns a context scoped to the tenant: the caller's own, or, for
    a platform admin naming another, that tenant through `WithActingTenant`.
    Handlers read the tenant back (`apiutil.ActingContext`), and RLS, storage
    keys and the Cedar resource all follow that one value.
  - **The role is the gate; Cedar decides on the target's policies.** Only a
    platform admin gets past the tenant check to another tenant, the same role
    the admin plane checks. Cedar then evaluates the operation against the
    target tenant's policy set, so the target's forbids apply to the admin.
    The admin's principal keeps its own tenant: it is never made a member of
    the target, so the target's member permits do not reach it.
  - **One request, one tenant.** Every name in a request must name the same
    tenant; a copy or batch spanning two is refused. Version names and the
    multipart completion name, which bypassed the tenant check, go through it.
  - **Uploads count against the target's quotas.** The quota interceptor runs
    before the names are parsed, so it resolves the same tenant itself.
  - **The console acts on the routed tenant.** Pages under `/tenants/<id>/`
    name that tenant; the user's own is used only elsewhere.

- **Consequences.**
  - A platform admin can browse and change any tenant's objects from that
    tenant's pages, under that tenant's policies and quotas.
  - A member naming another tenant in a version name gets `PermissionDenied`;
    it used to be served its own tenant silently.
  - The tenant is decided before anything keys on it: `ActOnNamedTenant`
    reads the names ahead of the rate limiter and the idempotency store, so
    both count the admin's call against the target. (Until 2026-10-05 both
    ran on the admin's own tenant.)
  - The admin's data-plane work inside the target is audited and shows in the
    target's trail (`AuditActingElsewhere`). A tenant's own principals' data-
    plane writes are not audited; that is recorded in BACKLOG.
