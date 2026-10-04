// Package auth carries authenticated identity through the request context.
//
// Tenant identity is propagated as an opaque value via context — never via
// request fields. Handlers read the tenant via TenantFromContext.
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Principal is the authenticated caller.
type Principal struct {
	// TenantID is the tenant the caller is scoped to. Empty only for
	// super-admin principals invoking cross-tenant RPCs.
	TenantID uuid.UUID
	// TenantSlug is the tenant's human-readable slug. Optional during the
	// rollout — when set, becomes the canonical Cedar Tenant UID. Carried
	// from the JWT `tenant_slug` claim minted by the issuer.
	TenantSlug string
	// Subject is the stable identifier inside the tenant (user ID, service
	// account, etc.). Carried verbatim from the JWT `sub` claim.
	Subject string
	// Roles are RBAC strings used by Cedar policies (e.g. "tenant.admin",
	// "platform.admin", "mcp.operator").
	Roles []string
	// Scopes narrow the principal to a subset of resources. Cedar evaluates
	// resource membership against these in addition to roles. Empty means
	// "no per-resource restriction" (Cedar default policies still apply).
	Scopes []Scope
	// Audience is the JWT `aud` claim — used by handlers to assert that a
	// caller hitting the admin plane wasn't issued a data-plane token.
	Audience string
	// Audiences is every value of the token's `aud` claim, whatever the
	// verifier expected. A caller that forwards the token — the MCP edge —
	// reads it to know which planes the token can reach.
	Audiences []string
	// ExpiresAt is the token's `exp`; zero when the claim is absent.
	ExpiresAt time.Time
	// PrincipalKind distinguishes a User-bound JWT from an ApiKey-derived
	// token. Cedar policies may key on this for blast-radius limits.
	Kind PrincipalKind
	// Labels are free-form claim attributes exposed to Cedar as principal
	// attributes.
	Labels map[string]string
}

// PrincipalKind enumerates the originating credential type.
type PrincipalKind uint8

const (
	PrincipalKindUnspecified PrincipalKind = iota
	PrincipalKindUser
	PrincipalKindApiKey
	PrincipalKindServiceAccount // platform-issued, e.g. MCP server
	// PrincipalKindCapability is a principal established by a verified
	// capability token: short-lived, tenant-scoped, individually revocable,
	// and carrying no roles. It authenticates its bearer AS the tenant the
	// capability was issued for, which is what lets one service credential
	// mint per-tenant access without holding a long-lived credential per
	// tenant. Authorisation beyond identity stays with the capability's own
	// caveats (ops, resource prefixes, budget) — enforced by the interceptor
	// before this principal is ever established — and with Cedar, whose
	// data-plane policies gate on tenant membership rather than on roles.
	PrincipalKindCapability
)

// Wire names for PrincipalKind, as Cedar sees them in `principal.kind`.
//
// Stable strings, not the numeric values: they appear in tenant-authored
// policies, so renaming one silently changes what those policies match. An
// unspecified kind is the EMPTY string — a policy comparing against it matches
// nothing, which is the fail-closed default for a principal built by a path
// that has not been taught to carry its kind.
const (
	PrincipalKindNameUser           = "user"
	PrincipalKindNameApiKey         = "api_key"
	PrincipalKindNameServiceAccount = "service_account"
	PrincipalKindNameCapability     = "capability"
)

// String returns the wire name Cedar policies match on.
func (k PrincipalKind) String() string {
	switch k {
	case PrincipalKindUser:
		return PrincipalKindNameUser
	case PrincipalKindApiKey:
		return PrincipalKindNameApiKey
	case PrincipalKindServiceAccount:
		return PrincipalKindNameServiceAccount
	case PrincipalKindCapability:
		return PrincipalKindNameCapability
	case PrincipalKindUnspecified:
		return ""
	default:
		return ""
	}
}

// IsMachine reports whether the credential behind this principal belongs to a
// service rather than to a person. Machine principals are provisioned
// deliberately — a platform admin mints an API token, a capability-issuer mints
// a capability — and they act on storage as the consumer that owns it, which is
// why the built-in policy trusts them with operations a human user must hold a
// role for.
func (k PrincipalKind) IsMachine() bool {
	switch k {
	case PrincipalKindApiKey, PrincipalKindServiceAccount, PrincipalKindCapability:
		return true
	case PrincipalKindUser, PrincipalKindUnspecified:
		return false
	default:
		return false
	}
}

// HasRole reports whether the principal carries the given role string.
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type principalKey struct{}

// WithPrincipal returns a child context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the authenticated principal or an error when
// unset. Handlers should return connect.CodeUnauthenticated on error.
func PrincipalFromContext(ctx context.Context) (*Principal, error) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	if !ok || p == nil {
		return nil, ErrNoPrincipal
	}
	return p, nil
}

// TenantFromContext is a shortcut for handlers that only need the tenant ID.
// Returns ErrNoPrincipal when unauthenticated and ErrNoTenant when the
// principal is a cross-tenant admin without a tenant binding.
func TenantFromContext(ctx context.Context) (uuid.UUID, error) {
	p, err := PrincipalFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if p.TenantID == uuid.Nil {
		return uuid.Nil, ErrNoTenant
	}
	return p.TenantID, nil
}

var (
	ErrNoPrincipal = errors.New("auth: no authenticated principal")
	ErrNoTenant    = errors.New("auth: principal is not bound to a tenant")
)

// ─── Acting on another tenant's behalf ──────────────────────────────────────

type actingTenantKey struct{}

// WithActingTenant marks the request as operating on `tenantID`'s data rather
// than the caller's own. The RLS pool sets `paladin.tenant_id` from this value
// when present, so every query on the derived context — reads and writes
// alike — is scoped to that tenant instead of the caller's.
//
// This is what lets the admin plane work — and the data plane, for a platform
// admin naming another tenant's objects (connectshim/data scopeToTenant,
// ADR-0022): a platform admin's principal is bound to the `platform` tenant,
// but the resources it manages belong to someone else. Without it a cross-tenant write trips WITH CHECK, and a
// cross-tenant read silently returns nothing — RLS filters rather than
// errors, so the second failure looks like an empty list.
//
// # It authorises nothing
//
// Call it only AFTER the Cedar check that permits this caller to act on this
// tenant, and only with the tenant that check was performed against. Calling
// it earlier, or with a tenant taken from somewhere other than the authorised
// resource name, hands the request another tenant's data — this function is
// the mechanism that RLS otherwise denies, so the gate in front of it is the
// whole protection.
func WithActingTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	if tenantID == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, actingTenantKey{}, tenantID)
}

// ActingTenant returns the tenant this request is acting on behalf of, and
// whether one was set.
func ActingTenant(ctx context.Context) (uuid.UUID, bool) {
	tid, ok := ctx.Value(actingTenantKey{}).(uuid.UUID)
	return tid, ok && tid != uuid.Nil
}

// EffectiveTenant is the tenant whose rows this request may touch: the one it
// is acting on behalf of, or failing that the caller's own. This is the value
// the RLS pool binds to `paladin.tenant_id`.
func EffectiveTenant(ctx context.Context) (uuid.UUID, error) {
	if tid, ok := ActingTenant(ctx); ok {
		return tid, nil
	}
	return TenantFromContext(ctx)
}

type crossTenantReadKey struct{}

// WithCrossTenantRead marks the request as a platform-wide READ. The RLS
// pool sets `paladin.cross_tenant` from it, which the policies admit in
// USING and never in WITH CHECK — so it can widen what a query sees and
// cannot let it write outside one tenant.
//
// Same rule as WithActingTenant: call it only after the check that
// establishes the caller may read across tenants (today, the
// platform.admin gate). It is the mechanism RLS otherwise denies.
//
// Prefer WithActingTenant when the request names one tenant. This is for
// the surfaces that genuinely span them — the storage browser listing every
// collection bound to a bucket, whoever owns it.
func WithCrossTenantRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, crossTenantReadKey{}, true)
}

// CrossTenantRead reports whether this request was marked platform-wide.
func CrossTenantRead(ctx context.Context) bool {
	v, _ := ctx.Value(crossTenantReadKey{}).(bool)
	return v
}
