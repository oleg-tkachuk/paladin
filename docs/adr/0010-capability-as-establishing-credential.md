# ADR-0010: A capability may establish identity on the data plane

- **Status:** Accepted — implemented 2026-08-17 (`PrincipalKindCapability`,
  `CapabilityEstablishingInterceptor`, data-plane wiring, tests). Extends the
  capability subsystem introduced with `migrations/016_capabilities.sql`.
- **Context:** capabilities were **additive only**. The interceptor verified the
  token, stashed it on the context, and handlers could narrow behaviour by it —
  but nothing derived a `Principal` from it (`PrincipalKind` had no capability
  member, and only `middleware/audit.go` and the capability handler ever read
  `CapabilityFromContext`). The consequence: a capability could restrict a
  caller who was **already** authenticated by JWT or API token, and could never
  authenticate one on its own.

  That made capabilities unusable for the case they are shaped for. A consumer
  such as consumer serves many tenants from one process. A Paladin API token is
  bound to one tenant for its whole life — the principal derived from it carries
  no roles, the only cross-tenant bypass is role-based, and `tenant:` scopes are
  read by no authorization site — so serving N tenants meant holding N
  long-lived credentials, one per tenant, at rest in the consumer. The
  alternative was a single credential with cross-tenant reach, whose leak
  exposes every tenant.

  A capability is the primitive that solves this: short-lived, signed,
  individually revocable, and it already names its tenant in a
  signature-covered subject. What was missing was that presenting one granted
  nothing.

- **Decision:** on the **data plane**, a verified capability establishes the
  caller's identity when the request carries no other credential.

  `principalFromCapability` maps the capability's own subject tenant to
  `Principal.TenantID`, sets `Subject` to `capability:<id>` so audit can trace
  the exact grant, and sets `Kind = PrincipalKindCapability`. It attaches **no
  roles**, deliberately: a capability names a tenant, not a role, and must never
  satisfy a role-gated admin policy. The data plane's Cedar policies gate on
  tenant membership (`principal in Tenant::…`), so a tenant-scoped principal is
  exactly what they need.

  Three properties keep this safe:

  1. **Authorization is unchanged.** What the bearer may DO is still decided by
     the caveats (ops, resource prefixes, budget, source IP), enforced in
     `enforceCaveats` **before** any principal is established, and then by
     Cedar.
  2. **An existing principal always wins.** A capability presented alongside a
     JWT or API token stays additive, so adding one to an existing call cannot
     re-scope it to another tenant.
  3. **Only the data plane grants it.** `CapabilityEstablishingInterceptor` is a
     separate constructor from the additive one so the grant is visible at the
     wiring site. Admin and IAM keep the additive interceptor: establishing
     there would produce a role-less caller for role-gated RPCs — refused, but
     for a confusing reason.

- **Alternatives considered:**
  - **Roles on API tokens.** Would let one token serve many tenants via a
    role-gated bypass. Rejected: it makes a long-lived secret cross-tenant,
    which is the blast radius capabilities exist to avoid.
  - **Authorize by `tenant:` scope.** The scope type parses but no authorization
    site consumes it; making it authoritative would be a second, parallel
    tenancy mechanism next to the one in `assertJWTTenant`.
  - **Leave capabilities additive and let consumers hold N credentials.** Works
    today and is what consumer did with one; at per-account tenancy it means a
    credential store the size of the user base.

- **Consequences:**
  - A consumer can hold one issuing identity and mint per-tenant, short-lived,
    revocable access. Nothing per-tenant sits at rest on its side.
  - **The signing key must be persistent.** `capability.signing_key_path`
    unset generates an ephemeral keypair, and the server already warns that
    restarts invalidate every issued token. Tolerable while capabilities were a
    narrowing extra; not tolerable once they authenticate. Deployments must
    mount a real key.
  - Revocation latency is `capability.revocation_cache_ttl` (2s by default) —
    the window in which a revoked capability still authenticates.
  - Audit rows now carry `capability:<id>` as the subject for these calls, which
    is more precise than the API-token subject it replaces.
