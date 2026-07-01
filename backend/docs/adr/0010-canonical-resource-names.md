# ADR-0010: Canonical resource names for ObjectKey-rooted resources (A + B + C)

- **Status:** Accepted 2026-07-01 — ratifies the previously *proposed* design
  in [docs/canonical-resource-names.md](../canonical-resource-names.md)
  (Status/Owner had been "proposed / TBD"). This ADR is the authoritative
  record; the plan doc remains the detailed phase-by-phase reference.
  Implementation is **phased and mostly not yet done** — see *Implementation
  status* below. This ADR exists so the remaining phases can be executed
  coherently instead of piecemeal, which the invariants forbid.

- **Context.** An ObjectKey-rooted resource is addressable three ways today,
  and the codebase is inconsistent about which it uses where:

  | Shape | Example | Used in |
  |---|---|---|
  | **A — canonical** | `storageBackends/{b}/buckets/{bk}/tenants/{tid}/objectKeys/{ok}` | (target) DB rows, audit, event payloads, Cedar `resource ==` |
  | **C — tenant-first** | `tenants/{tid}/objectKeys/{ok}` | operator UI, admin console, the whole current API surface |
  | **B — bare** | `objectKeys/{ok}` or `{ok}` | (future) CLI/SDK ergonomics via a per-tenant default binding |

  The physical S3 layout (`<bucket>/<tenant_id>/<object_key>/<user_key>`) is
  unaffected — presign signs `(bucket, key)`, not the API name — so all three
  shapes address the same bytes.

  Concrete state discovered while ratifying this (do not re-derive):
  - The **central resolver already exists** (`connectshim/resolve.
    ResolveObjectKeyName`) and every admin objectKey call site uses it. That
    is the plan's **Phase 2** — done. It also carries the cross-tenant guard
    (C/B `tid` must equal the JWT tenant unless platform.admin).
  - The **Cedar ObjectKey EUID is not canonical and not even the C-shape the
    old notes claimed.** `engine.go::objectKeyUID(tid, ok)` emits
    `ObjectKey::"{tenant_uuid}/{object_key}"`. The canonical A-shape needs the
    **backend + bucket**, which `objectKeyUID` does not receive and which most
    authz call sites leave empty (`cedar.Resource{BackendID:"", BucketName:""}`).
    So "canonical Cedar EUID" is not a string tweak — it requires threading
    `(backend, bucket)` into the resource at every objectKey authz site (a DB
    binding lookup on the authz path), or enriching it in the resolver.
  - **Deployed operator policies do not hardcode ObjectKey resource EUIDs.**
    The default tenant template and the one deployed custom policy use
    `Tenant::"<slug>"` (principal group) + unconstrained `resource` with
    attribute conditions — no `resource == ObjectKey::"…"` literals. So the
    policy-rewrite risk of an EUID-shape change is low *for what is deployed*,
    but the rewrite pass must still be safe for any future literal.
  - The **shape-distribution metric** `paladin_resource_name_shape_total{shape}` is
    wired over OTLP (since 2026-06-29). It has no representative data yet.

## Decision

Adopt the A+B+C model with A as the single canonical form, and execute it in
the plan's five phases. The four invariants are binding and are the reason the
work cannot be shipped one surface at a time:

1. **One canonical form per resource.** Every *persisted* ref — audit
   `resource_name`, event `resource_name`, resource-version tokens, pagination
   tokens, idempotency keys, Cedar resource literals — is canonical (A).
   Aliases (B/C) never persist.
2. **Resolve before authorize.** The Cedar evaluator sees only canonical;
   B/C → A resolution happens *before* the policy gate, never after.
3. **No mixed shapes.** Reject hybrid inputs (e.g. an A prefix without the
   `tenants/…` middle). A 4th, ad-hoc shape is where cross-tenant bugs spawn.
4. **JWT tenant is authoritative.** When an alias supplies a tenant scope, the
   resolved canonical's `tid` MUST equal the JWT's `tenant_id`; mismatch → 403
   (`PERMISSION_DENIED`, not `NOT_FOUND`). Already enforced by the resolver.

### Phases (authoritative numbering; see plan doc for detail)

- **Phase 1 — Canonical (A) internally.** Make A the source of truth without
  changing the public (C) contract. This is the next unit of work and the
  larger one: `ObjectKey.CanonicalName()`; audit + `event_deliveries`
  `resource_name` writes switch to A with a backfill migration; the Cedar
  ObjectKey EUID becomes canonical — which entails threading `(backend, bucket)`
  into the authz resource (or resolver-enriching it) **and** a rewrite pass
  over `tenants.inherited_cedar_policy` + `object_keys.cedar_policy` for any
  EUID literals; Cedar authoring docs updated. Because of Invariant 1 these
  land **together** (one deploy window), else authz sees A while audit/events
  see C — the mixed state Invariant 3 forbids.
- **Phase 2 — C-alias central resolver. DONE.** `ResolveObjectKeyName` +
  cross-tenant guard + the input-shape metric.
- **Phase 3 — B alias + `tenant_default_bindings`.** Bare-name ergonomics;
  new table + 3 RPCs + resolver extension + a settings UI tab.
- **Phase 4 — WhoAmI returns all three forms.** Clients never construct
  canonical themselves; the SDK normalizes to A before sending.
- **Phase 5 — soft-deprecate C on the wire (data-gated, distant).** Emit a
  deprecation header on C-shaped requests once metrics show A dominates.
  **Never drop C from the server** — the operator UI depends on it
  indefinitely. This is what the BACKLOG "Phase 3: deprecate redundant
  resource-name shapes" entry is really about, and it is gated on ≥1 week of
  real `paladin_resource_name_shape_total` traffic — a *deploy window does not
  substitute for that data*.

### What this ADR deliberately does NOT do

It does not implement any phase. It ratifies the direction, fixes the
authoritative phase numbering, and records the two non-obvious constraints
(canonical Cedar EUID needs backend+bucket; deprecation is data-gated) so the
next implementer does not discover them the hard way.

## Consequences

- **Positive.** One canonical form kills a class of cross-tenant / mixed-shape
  bugs; operators keep the readable C-shape; CLI/SDK get bare names; audit and
  event consumers get a single stable reference to join on.
- **Breaking (Phase 1).** Outbound event `resource_name` changes C → A — event
  subscribers must be told (changelog + a version bump on the sink config, per
  the CloudEvents work). Audit rows change shape after the backfill.
- **Cost.** Canonical Cedar EUID adds a `(backend, bucket)` binding lookup to
  the objectKey authz path (cacheable per objectKey). Phase 1 is a
  multi-surface change requiring a coordinated deploy + backfill, not a batch.
- **Rollback.** Each phase's migration is reversible (Down restores the prior
  `resource_name`/EUID form). Phase 1's Cedar-EUID switch is the only one that
  is authz-visible; gate it behind a shadow-eval (log old-vs-new decisions for
  a soak period before enforcing) so a divergence is caught before it denies.

## Alternatives considered

- **Do nothing / keep three shapes ad hoc.** Rejected — the inconsistency is
  already a latent cross-tenant hazard (Invariant 3) and blocks a stable
  audit/event join key.
- **Canonicalize Cedar EUID alone (the tempting "small" slice).** Rejected —
  violates Invariant 1 (cedar A-shape while audit/events stay C-shape) and
  still requires the backend+bucket threading, so it is neither small nor safe
  in isolation.
- **Federated resource naming / external registry.** Out of scope; PALADIN owns
  its namespace (consistent with [ADR-0006](0006-deferred-roadmap.md)).
