# ADR-0012: Cedar tenant membership keys on the DB-authoritative slug (independent cross-tenant isolation)

- **Status:** Accepted 2026-07-03 — surfaced by the adversarial security suite
  (PR #134) during the ADR-0011 dedicated-tenant live verification.
  Implementation lands with this ADR.

- **Context.** The rendered default policy keys member permits on the tenant's
  human-readable slug:

  ```cedar
  permit ( principal in Tenant::"acme", action in [...], resource );
  ```

  `Engine.buildEntities` builds the Cedar entity graph. Two facts about the
  pre-ADR graph made Cedar unable to enforce cross-tenant isolation on its own:

  1. **The User was anchored under the RESOURCE's tenant**, not the
     principal's (`userParents = { tUID }`, where `tUID` is the resource's
     Tenant UID). So `principal in Tenant::"X"` was *tautologically true*
     whenever the resource belonged to tenant X — for **any** caller.
  2. **The Tenant UID was keyed on the slug carried in the request**
     (`r.TenantSlug`, and after PR #132 an own-tenant inherit of
     `p.TenantSlug`). Both trace back to the JWT `tenant_slug` claim, which is
     **attacker-controlled** — no code path validated it against the DB.

  Cross-tenant isolation therefore rested entirely on ONE upstream guard: the
  data-plane shim's `assertJWTTenant` (URL tenant must equal the JWT tenant for
  non-admins), plus `compiledFor` loading the policy by the **trusted** tenant
  UUID. That composition is correct today — a spoofed `tenant_slug` fails to
  match the real slug in the policy loaded for the trusted UUID, so it *fails
  closed*. But it is defense-in-**composition**, not defense-in-**depth**: a
  single new endpoint that builds a `cedar.Resource` for a foreign tenant and
  skips `assertJWTTenant` would grant cross-tenant access, and Cedar — the
  component whose entire job is authorization — would not stop it.

  A tempting "fix" (PR #134, reverted) anchored membership on the principal's
  own `tenant_slug`. That is **strictly worse**: the slug is attacker-supplied,
  so a caller could claim a victim's slug and satisfy a member permit. The
  lesson: **membership must never key on a JWT-supplied slug.**

  `tenants.slug` is `NOT NULL` and immutable post-create (migrations 009 / 033),
  so a DB-authoritative slug is always available and stable.

## Decision

Make Cedar an **independent** second isolation layer by keying tenant
membership on the **DB-authoritative** slug and the **trusted** tenant UUID —
never the JWT slug.

1. **The policy Store returns the tenant's authoritative slug** alongside the
   policy text. `PostgresStore.Fetch` already reads the `tenants` row for
   `inherited_cedar_policy`; it now also selects `tenants.slug`. The slug is
   cached with the compiled policy (same TTL + `policy_changed` invalidation),
   so there is no extra query on the authz hot path.

2. **The resource Tenant entity is keyed on the authoritative slug** (from the
   fetch for `r.TenantID`), not `r.TenantSlug`. A spoofed request slug is
   ignored for the entity UID.

3. **User membership anchors on the principal's tenant, gated by trusted-UUID
   equality.** The User is placed under the resource's Tenant entity **only
   when `p.TenantID == r.TenantID`** (both UUIDs, both trusted). When they
   differ, the User is anchored under the principal's own UUID-keyed Tenant
   (`Tenant::"<p.TenantID>"`), which cannot match a slug-keyed member permit.

Consequences of the invariants together:

- **Same-tenant member** (the only case a non-admin reaches past
  `assertJWTTenant`): `compiledFor(r.TenantID)` loads the tenant's policy and
  its authoritative slug; the User is anchored under that slug; the member
  permit matches → allow. A spoofed `tenant_slug` is irrelevant.
- **Cross-tenant, member permit** (reachable only past the shim — a bug, or a
  platform-admin): the User is anchored under a *different* tenant's UID, so
  `principal in Tenant::"<resource-slug>"` never matches → **deny at Cedar**.
- **Cross-tenant, role permit** (`platform.admin`): unaffected — the builtin
  policy grants by `principal.roles`, not membership, so legitimate admin
  cross-tenant reach is preserved.

PR #132's own-tenant slug inherit in `IsAuthorized` is **removed** — the
authoritative slug supersedes it.

### What this ADR deliberately does NOT do

- **Does not re-key policies by tenant UUID.** Operators keep the readable
  `Tenant::"acme"` form; only the *source* of the slug changes (DB, not JWT).
- **Does not remove `assertJWTTenant`.** The shim stays the first line; Cedar
  becomes a genuine second. Two independent layers, not one.

## Consequences

- **Positive.** Cross-tenant isolation no longer depends on a single guard.
  A future endpoint that forgets `assertJWTTenant` is still stopped by Cedar
  for membership-scoped permits. The attacker-controlled `tenant_slug` claim is
  removed from the authz decision entirely.
- **Behavior change (intended).** Cross-tenant access via a *member* permit is
  now denied at the Cedar layer. No such legitimate flow exists (cross-tenant
  is platform-admin, i.e. role-based); verified against the deployed cluster
  and the adversarial suite.
- **Cost.** One extra column in the policy fetch (same row, no extra query);
  the slug rides the existing compiled-policy cache. `Store.Fetch` gains a
  return value — a small signature change across the one impl and the test
  fakes.
- **Rollback.** Revert the commit. The pre-ADR behavior (membership under the
  resource tenant, JWT slug) returns; isolation falls back to `assertJWTTenant`
  alone, which is the current production posture.

## Alternatives considered

- **Do nothing — rely on `assertJWTTenant`.** Rejected: single-guard isolation
  for the authorization system is a latent hazard; the adversarial suite shows
  Cedar would grant cross-tenant if the guard is ever bypassed.
- **Anchor membership on the principal's JWT slug.** Rejected and reverted
  (PR #134): the slug is attacker-controlled — strictly worse than the status
  quo.
- **Re-key all policies and the Cedar Tenant UID by tenant UUID.** Rejected:
  a breaking migration of every deployed policy for no security gain over the
  DB-authoritative slug, and it loses the human-readable `Tenant::"acme"` form.
