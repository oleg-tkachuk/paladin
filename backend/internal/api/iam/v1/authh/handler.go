// Package authh implements the IAM AuthService — login, refresh, who-am-I,
// password change. The handler is plane-agnostic Go (no Connect imports);
// connectshim/iam wraps it for the wire layer.
package authh

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// reuseAuditor records refresh-token reuse to the audit log (admin console).
// Satisfied by admindomain.AuditRepository; optional.
type reuseAuditor interface {
	Insert(ctx context.Context, e admindomain.AuditEntry) error
}

// RefreshTokenDecoder verifies a refresh-token signature and extracts the
// jti / user_id / tenant_id claims. Implemented by the auth package using
// the IAM-audience JWT verifier; we inject it as an interface so the
// handler doesn't depend on JWT internals.
type RefreshTokenDecoder interface {
	DecodeRefresh(token string) (jti, userID, tenantID uuid.UUID, err error)
}

// Handler exposes login + token lifecycle. PolicyEngine is consulted on
// audience escalation (RefreshToken with requested_audience=paladin-admin).
// TenantSlugLookup resolves a tenant UUID to its kebab-case slug. Optional
// at construction — when nil, minted access tokens omit the `tenant_slug`
// claim and Cedar policies fall back to UUID-keyed Tenant UIDs.
type TenantSlugLookup func(ctx context.Context, tenantID uuid.UUID) (string, error)

// tokenMinter is the slice of *issuer.Issuer this handler needs — minting the
// access + refresh JWT pair. Declared as an interface so the handler is
// unit-testable with a stub; the concrete *issuer.Issuer satisfies it
// implicitly, so call sites are unchanged.
type tokenMinter interface {
	MintAccess(c issuer.AccessClaims) (string, time.Time, error)
	MintRefresh(c issuer.RefreshClaims) (string, time.Time, error)
}

// CollectionRoute is one addressable Collection in all three ADR-0010 name
// shapes (A canonical, C tenant-path, B bare alias) plus its (backend,
// bucket) binding. WhoAmI returns these so clients normalize to canonical
// before sending rather than constructing it themselves (Phase 4).
type CollectionRoute struct {
	Canonical  string // A
	TenantPath string // C
	BareAlias  string // B — empty unless this OK sits in the default binding
	Backend    string
	Bucket     string
}

// CollectionRouteLister returns the caller's Collection route table for a
// tenant — the Collections the caller can read, in all three name shapes.
// Optional dependency (WithCollectionRoutes); when unset WhoAmI returns no
// routes. Lives behind an interface so the plane-agnostic auth handler stays
// free of admin-plane (collection / tenant-binding) imports; the concrete
// implementation is wired in the composition root.
type CollectionRouteLister interface {
	// Returns one page of the route table (starting after pageToken; empty =
	// first page) plus nextPageToken — non-empty when more readable Collections
	// remain, so the caller pages until it comes back empty (ADR-0010 Phase 4).
	ListCollectionRoutes(ctx context.Context, tenantID uuid.UUID, pageToken string) (routes []CollectionRoute, nextPageToken string, err error)
}

type Handler struct {
	users          authstore.UserRepository
	refresh        authstore.RefreshTokenRepository
	issuer         tokenMinter
	refreshDecoder RefreshTokenDecoder
	policy         cedar.Authorizer
	tenantSlug     TenantSlugLookup
	now            func() time.Time

	// Optional: WhoAmI Collection route table (ADR-0010 Phase 4). nil → no
	// routes in the response.
	routes CollectionRouteLister

	// Optional: refresh-token reuse-detection observability.
	audit reuseAuditor
	log   *zap.Logger
}

// WithCollectionRoutes installs the source of the WhoAmI Collection route table
// (ADR-0010 Phase 4). Builder-style + optional so existing wire-up and tests
// keep working; unset means WhoAmI returns identity with no routes.
func (h *Handler) WithCollectionRoutes(l CollectionRouteLister) *Handler {
	h.routes = l
	return h
}

// WithReuseAudit wires the audit log + logger used to surface refresh-token
// reuse events. Optional; both nil → revoke-only (no audit row / log).
func (h *Handler) WithReuseAudit(a reuseAuditor, l *zap.Logger) *Handler {
	h.audit = a
	h.log = l
	return h
}

func NewHandler(
	users authstore.UserRepository,
	refresh authstore.RefreshTokenRepository,
	iss tokenMinter,
	dec RefreshTokenDecoder,
	policy cedar.Authorizer,
) *Handler {
	return &Handler{
		users:          users,
		refresh:        refresh,
		issuer:         iss,
		refreshDecoder: dec,
		policy:         policy,
		now:            time.Now,
	}
}

// WithTenantSlugLookup installs the resolver used to populate the
// `tenant_slug` access-token claim. Builder-style so existing wire-up
// callers keep working without breakage.
func (h *Handler) WithTenantSlugLookup(f TenantSlugLookup) *Handler {
	h.tenantSlug = f
	return h
}

// ─── Login ──────────────────────────────────────────────────────────────────

type LoginInput struct {
	Subject           string
	Password          string
	UpstreamCode      string // unused for local IdP
	RequestedAudience string // empty → AudienceData
	// TenantHint optionally narrows the lookup to one tenant. Empty means
	// "lookup across all tenants by subject" — server picks the only match
	// or returns InvalidArgument when ambiguous.
	TenantHint uuid.UUID
}

type LoginOutput struct {
	User             authstore.User
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	// Audience actually minted into AccessToken — the resolved value, not
	// what was asked for. RequestedAudience defaults when empty, so the two
	// differ routinely and the caller has no other way to tell.
	Audience string
}

func (h *Handler) Login(ctx context.Context, in LoginInput) (*LoginOutput, error) {
	if in.Subject == "" {
		metrics.RecordLoginAttempt(ctx, "invalid_argument")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subject required"))
	}
	if in.Password == "" {
		metrics.RecordLoginAttempt(ctx, "invalid_argument")
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("password required"))
	}

	u, err := h.resolveLoginUser(ctx, in)
	if err != nil {
		// The outcome is derived from the Connect code rather than passed down
		// from resolveLoginUser, because that function deliberately returns ONE
		// generic "invalid credentials" for several distinct failures so as not
		// to leak whether a subject exists. Reading the code back keeps the
		// metric on the same side of that line: it can say how many logins
		// failed, and cannot say which subjects.
		metrics.RecordLoginAttempt(ctx, loginOutcome(err))
		return nil, err
	}

	audience := in.RequestedAudience
	if audience == "" {
		audience = auth.AudienceData
	}
	if err := h.assertAudienceAllowed(u, audience); err != nil {
		metrics.RecordLoginAttempt(ctx, "audience_denied")
		return nil, err
	}

	access, refresh, accessExp, refreshExp, err := h.mintPair(ctx, u, audience, uuid.Nil)
	if err != nil {
		metrics.RecordLoginAttempt(ctx, "error")
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	metrics.RecordLoginAttempt(ctx, "ok")

	// The one write on `users` that had no tenant on the context: login runs
	// before a session exists. But by this line the credential has been
	// verified and `u` IS the user, so the tenant is known — pinning it here
	// costs nothing and removes the last pre-session write from the table.
	// What login still needs from a policy is a pre-auth READ, to find this
	// row in the first place; the write no longer needs an exemption.
	if err := h.users.TouchLogin(auth.WithActingTenant(ctx, u.TenantID), u.UserID, h.now()); err != nil {
		// Non-fatal — login succeeded and must not be undone by a bookkeeping
		// write. But last_login_at is what an audit answers "when did this
		// account last sign in" with, so it failing silently turns a
		// compliance field into a lie nobody can date.
		logger.FromContext(ctx).Warn("last_login_at not stamped on login",
			zap.String("user_id", u.UserID.String()), zap.Error(err))
	}

	return &LoginOutput{
		User:             u,
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		Audience:         audience,
	}, nil
}

// ─── RefreshToken (token-exchange / RFC 8693) ───────────────────────────────

type RefreshInput struct {
	RefreshToken      string
	RequestedAudience string // empty → keep audience of the access token paired with this refresh; v2 requires explicit
}

type RefreshOutput struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	// Audience actually minted into AccessToken. See LoginOutput.Audience.
	Audience string
}

func (h *Handler) RefreshToken(ctx context.Context, in RefreshInput) (*RefreshOutput, error) {
	if in.RefreshToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("refresh_token required"))
	}
	jti, userID, tenantID, err := h.parseRefresh(in.RefreshToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	stored, err := h.refresh.Get(ctx, jti)
	if err != nil {
		// Replay of a rotated (revoked) token → RFC 6819 theft signal: revoke
		// the whole family and record it before rejecting. UNLESS the token
		// was superseded moments ago, which is not theft but a sibling that
		// rotated first — see errRotatedBySibling.
		if errors.Is(err, authstore.ErrTokenRevoked) {
			if any, gerr := h.refresh.GetAny(ctx, jti); gerr == nil && h.withinSupersessionGrace(any) {
				h.onRefreshRaceLost(ctx, jti, userID)
				return nil, errRotatedBySibling
			}
			h.onRefreshReuse(ctx, jti, userID, tenantID)
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token rejected"))
		}
		if errors.Is(err, authstore.ErrNotFound) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token rejected"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if h.now().After(stored.ExpiresAt) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token expired"))
	}

	u, err := h.users.GetByID(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user no longer exists"))
	}
	if u.Disabled || u.TenantID != tenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("user no longer eligible"))
	}

	audience := in.RequestedAudience
	if audience == "" {
		audience = auth.AudienceData
	}
	if err := h.assertAudienceAllowed(u, audience); err != nil {
		return nil, err
	}

	// Rotation: SUPERSEDE the presented refresh — revoked, and marked as traded
	// in by its own holder rather than killed for cause — then mint a fresh
	// pair in the SAME family so the chain stays linked for reuse-detection.
	//
	// The distinction is what ExchangeAudience reads below. A bare Revoke here
	// would make this token indistinguishable from one revoked by logout or by
	// reuse detection, and the tolerance window could not exist.
	if err := h.refresh.Supersede(auth.WithActingTenant(ctx, stored.TenantID), jti); err != nil {
		// Lost the race: a sibling superseded this token between our read and
		// our write. The database decided it, so the decision holds across
		// every replica and nothing had to be shared to reach it.
		if errors.Is(err, authstore.ErrAlreadyRotated) {
			h.onRefreshRaceLost(ctx, jti, userID)
			return nil, errRotatedBySibling
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	access, newRefresh, accessExp, refreshExp, err := h.mintPair(ctx, u, audience, stored.FamilyID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return &RefreshOutput{
		AccessToken:      access,
		RefreshToken:     newRefresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		Audience:         audience,
	}, nil
}

// ─── ExchangeAudience ───────────────────────────────────────────────────────
//
// Derives a short-lived access token for a different audience from a
// still-valid refresh token, WITHOUT rotating the refresh chain. This is
// what a multi-plane SPA wants: one login, one refresh-token cookie, but
// simultaneous access tokens for paladin-data, paladin-admin and paladin-iam.
//
// The contract differs from RefreshToken in two ways:
//
//   1. The presented refresh token is validated (signature, store
//      presence, expiry) but is NOT revoked. Subsequent ExchangeAudience
//      calls re-use it.
//   2. No refresh token is returned — the caller already has one.
//
// Same audience-escalation policy as Login/RefreshToken: a user with only
// `tenant.user` cannot exchange into paladin-admin.

type ExchangeAudienceInput struct {
	RefreshToken   string
	TargetAudience string
}

type ExchangeAudienceOutput struct {
	AccessToken     string
	AccessExpiresAt time.Time
}

func (h *Handler) ExchangeAudience(ctx context.Context, in ExchangeAudienceInput) (*ExchangeAudienceOutput, error) {
	if in.RefreshToken == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("refresh_token required"))
	}
	if in.TargetAudience == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("target_audience required"))
	}

	jti, userID, tenantID, err := h.parseRefresh(in.RefreshToken)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	stored, err := h.refresh.Get(ctx, jti)
	if err != nil {
		if errors.Is(err, authstore.ErrTokenRevoked) {
			// Superseded a moment ago means the caller is racing its OWN
			// rotation, not replaying a stolen token: a browser cannot update
			// its cookie between two requests already in flight, so the token
			// it sends here is the one its sibling request just traded in.
			//
			// This RPC consumes nothing — it mints an access token and leaves
			// the chain untouched — so honouring it inside a narrow window
			// costs no rotation guarantee. What it buys is that the console's
			// BFF no longer has to remember rotations in process memory to
			// paper over the race, which is the single reason that process
			// cannot run in more than one replica.
			//
			// Only supersession qualifies. A token revoked for cause — logout,
			// or reuse detection killing the family — has superseded_at NULL
			// and is refused here exactly as before.
			if tok, gerr := h.refresh.GetAny(ctx, jti); gerr == nil && h.withinSupersessionGrace(tok) {
				stored, err = tok, nil
			} else {
				h.onRefreshReplayed(ctx, jti, userID, tenantID)
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token rejected"))
			}
		}
	}
	if err != nil {
		if errors.Is(err, authstore.ErrNotFound) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token rejected"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if h.now().After(stored.ExpiresAt) {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token expired"))
	}

	u, err := h.users.GetByID(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("user no longer exists"))
	}
	if u.Disabled || u.TenantID != tenantID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("user no longer eligible"))
	}

	if err := h.assertAudienceAllowed(u, in.TargetAudience); err != nil {
		return nil, err
	}

	// Mint access only — refresh chain stays intact.
	var slug string
	if h.tenantSlug != nil && u.TenantID != uuid.Nil {
		slug, _ = h.tenantSlug(ctx, u.TenantID)
	}
	access, accessExp, err := h.issuer.MintAccess(issuer.AccessClaims{
		Subject:    u.UserID.String(),
		TenantID:   u.TenantID,
		TenantSlug: slug,
		Audience:   in.TargetAudience,
		Roles:      u.Roles,
		Scopes:     u.Scopes,
		Kind:       auth.PrincipalKindUser,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mint access: %w", err))
	}
	return &ExchangeAudienceOutput{
		AccessToken:     access,
		AccessExpiresAt: accessExp,
	}, nil
}

// ─── Revoke ─────────────────────────────────────────────────────────────────

func (h *Handler) Revoke(ctx context.Context, token string) error {
	if token == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("token required"))
	}
	jti, _, tokenTenant, err := h.parseRefresh(token)
	if err == nil {
		// Revoke the FAMILY, not the single token. A logout ends the session,
		// and the session is the family: one login starts one, every rotation
		// inherits it. Killing only the presented jti left every other member
		// live — including the predecessor a page load had just rotated away
		// from, which the supersession window then honoured for another thirty
		// seconds. The user had logged out and the session had not ended.
		//
		// This does NOT touch the user's other logins: each has its own family,
		// which is the distinction RevokeFamilyOf was built to preserve.
		_, _ = h.refresh.RevokeFamilyOf(auth.WithActingTenant(ctx, tokenTenant), jti)
		return nil
	}
	// Access tokens are stateless — we can't revoke without a denylist.
	// v2 returns OK regardless to stay idempotent; deny-list is a follow-up.
	return nil
}

// ─── WhoAmI ─────────────────────────────────────────────────────────────────

type WhoAmIOutput struct {
	User     authstore.User
	Audience string
	// Routes is one page of the caller's Collection route table (ADR-0010 Phase
	// 4). Empty when no route source is wired or the caller has no readable
	// Collections.
	Routes []CollectionRoute
	// RoutesTruncated is true when more readable Collections remain beyond this
	// page (equivalent to NextPageToken != ""). Kept for clients that don't page.
	RoutesTruncated bool
	// NextPageToken pages the route table: pass it back in the next WhoAmI to
	// fetch the following page. Empty when this is the last page.
	NextPageToken string
}

func (h *Handler) WhoAmI(ctx context.Context, routePageToken string) (*WhoAmIOutput, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	// Subject in JWT is the user_id (UUID) when the token was minted via Login.
	id, parseErr := uuid.Parse(p.Subject)
	if parseErr != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("malformed principal subject %q", p.Subject))
	}
	u, err := h.users.GetByID(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	out := &WhoAmIOutput{User: u, Audience: p.Audience}
	// Route table is best-effort: WhoAmI's primary job is identity, so a
	// route-source failure (incl. a Cedar denial for a caller who can't list
	// Collections) degrades to an empty table rather than failing the call.
	if h.routes != nil && u.TenantID != uuid.Nil {
		routes, nextToken, rErr := h.routes.ListCollectionRoutes(ctx, u.TenantID, routePageToken)
		if rErr != nil {
			logger.FromContext(ctx).Warn("whoami: object-key route lookup failed; returning identity without routes",
				zap.String("tenant_id", u.TenantID.String()),
				zap.Error(rErr))
		} else {
			out.Routes = routes
			out.NextPageToken = nextToken
			out.RoutesTruncated = nextToken != ""
		}
	}
	return out, nil
}

// ─── Memberships / tenant switch ─────────────────────────────────────────────

// Membership is one tenant the caller's subject belongs to. Under the
// 1:1-per-tenant user model a membership == a `users` row, so roles are
// per-membership (the caller can be platform.admin in one tenant and a plain
// user in another).
type Membership struct {
	TenantID   uuid.UUID
	TenantSlug string
	Roles      []string
	Disabled   bool
	Current    bool
}

// ListMyMemberships returns every tenant the caller's subject has a user row
// in. The caller is identified from their access token (Subject = user_id); we
// resolve that row's login subject, then scan all tenants for it.
func (h *Handler) ListMyMemberships(ctx context.Context, in ListMembershipsInput) ([]Membership, string, error) {
	cur, err := h.callerUser(ctx)
	if err != nil {
		return nil, "", err
	}
	afterCreated, afterID, err := decodeMembershipCursor(in.PageToken)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	limit := in.PageSize
	if limit <= 0 {
		limit = membershipDefaultPageSize
	}
	// One extra row to detect a further page without a second query.
	// Memberships are, by definition, the subject's rows in EVERY tenant,
	// read while the session is scoped to the current one. users is
	// RLS-covered as of 014, so the read declares itself cross-tenant.
	matches, err := h.users.ListMembershipsBySubject(
		auth.WithCrossTenantRead(ctx), cur.Subject, afterCreated, afterID, limit+1)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	var next string
	if len(matches) > int(limit) {
		matches = matches[:limit]
		last := matches[len(matches)-1]
		next = encodeMembershipCursor(last.CreatedAt, last.UserID)
	}
	out := make([]Membership, 0, len(matches))
	for _, m := range matches {
		var slug string
		if h.tenantSlug != nil && m.TenantID != uuid.Nil {
			slug, _ = h.tenantSlug(ctx, m.TenantID)
		}
		out = append(out, Membership{
			TenantID:   m.TenantID,
			TenantSlug: slug,
			Roles:      m.Roles,
			Disabled:   m.Disabled,
			Current:    m.TenantID == cur.TenantID,
		})
	}
	return out, next, nil
}

type SwitchTenantOutput struct {
	User             authstore.User
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	// Audience actually minted into AccessToken. See LoginOutput.Audience.
	Audience string
}

// SwitchTenant mints a fresh access+refresh pair scoped to targetTenantID,
// provided the caller's subject has a non-disabled user row there. No password
// re-check: the caller already proved identity via their access token and could
// log in to the target tenant directly, so switching escalates nothing. A new
// refresh family is started (this is a distinct session), so revoking the old
// tenant's session does not touch the new one.
func (h *Handler) SwitchTenant(ctx context.Context, targetTenantID uuid.UUID, requestedAudience string) (*SwitchTenantOutput, error) {
	if targetTenantID == uuid.Nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("target tenant required"))
	}
	cur, err := h.callerUser(ctx)
	if err != nil {
		return nil, err
	}
	p, _ := auth.PrincipalFromContext(ctx) // callerUser already validated it

	// Point lookup, not a scan. This used to walk every membership the
	// subject had — which broke when the underlying query was capped at five
	// rows, making the sixth and later tenants unreachable ("not a member" for
	// a membership that plainly existed). Asking about the ONE tenant in
	// question cannot regress that way, and does not depend on the listing
	// staying unbounded.
	var target *authstore.User
	// The whole point of a switch is to read a row in a DIFFERENT tenant than
	// the session's. WithActingTenant scopes the connection to the target for
	// this read, which is tighter than a cross-tenant widening: it sees that
	// tenant and no other.
	found, err := h.users.GetBySubject(
		auth.WithActingTenant(ctx, targetTenantID), targetTenantID, cur.Subject)
	switch {
	case err == nil:
		target = &found
	case errors.Is(err, authstore.ErrNotFound):
		// leave target nil — handled by the generic refusal below
	default:
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Same generic phrasing whether the tenant exists or the caller just isn't
	// a member — don't leak tenant existence.
	if target == nil {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("not a member of the target tenant"))
	}
	if target.Disabled {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("user is disabled in the target tenant"))
	}

	audience := requestedAudience
	if audience == "" {
		audience = p.Audience // keep the plane the caller was working in
	}
	if audience == "" {
		audience = auth.AudienceData
	}
	if err := h.assertAudienceAllowed(*target, audience); err != nil {
		return nil, err
	}

	access, refresh, accessExp, refreshExp, err := h.mintPair(ctx, *target, audience, uuid.Nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// SwitchTenant stamps last_login_at on the row in the TARGET tenant while
	// the session is still scoped to the current one — the same shape as the
	// GetBySubject above it.
	if err := h.users.TouchLogin(auth.WithActingTenant(ctx, target.TenantID), target.UserID, h.now()); err != nil {
		// Non-fatal — the switch succeeded. Same reasoning as the login path.
		logger.FromContext(ctx).Warn("last_login_at not stamped on tenant switch",
			zap.String("user_id", target.UserID.String()), zap.Error(err))
	}
	h.auditTenantSwitch(ctx, cur, *target)

	return &SwitchTenantOutput{
		User:             *target,
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
		Audience:         audience,
	}, nil
}

// callerUser resolves the authenticated caller's current user row from the
// access-token principal (Subject = user_id).
func (h *Handler) callerUser(ctx context.Context) (authstore.User, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, err)
	}
	id, perr := uuid.Parse(p.Subject)
	if perr != nil {
		return authstore.User{}, connect.NewError(connect.CodeInternal,
			fmt.Errorf("malformed principal subject %q", p.Subject))
	}
	u, err := h.users.GetByID(ctx, id)
	if err != nil {
		return authstore.User{}, connect.NewError(connect.CodeNotFound, err)
	}
	return u, nil
}

// auditTenantSwitch records a best-effort audit row so operator scope changes
// are observable. Never blocks or fails the switch.
func (h *Handler) auditTenantSwitch(ctx context.Context, from, to authstore.User) {
	if h.audit == nil {
		return
	}
	_ = h.audit.Insert(ctx, admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            h.now().UTC(),
		ActorSubject:  to.UserID.String(),
		ActorTenantID: to.TenantID,
		ActorAudience: auth.AudienceIAM,
		Action:        "iam.TenantSwitched",
		ResourceName:  "tenants/" + to.TenantID.String(),
	})
}

// ─── ChangePassword ─────────────────────────────────────────────────────────

func (h *Handler) ChangePassword(ctx context.Context, oldPw, newPw string) error {
	if newPw == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("new password required"))
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	id, err := uuid.Parse(p.Subject)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	u, err := h.users.GetByID(ctx, id)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	if err := auth.CheckPassword(u.PasswordHash, oldPw); err != nil {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid old password"))
	}
	hash, err := auth.HashPassword(newPw)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := h.users.UpdatePasswordHash(ctx, id, hash); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	// Defensive: invalidate all refresh tokens on password change.
	_, _ = h.refresh.RevokeForUser(ctx, id)
	return nil
}

// ─── internals ──────────────────────────────────────────────────────────────

// assertAudienceAllowed prevents privilege escalation: a user without
// admin-tier roles cannot mint a paladin-admin token. The Cedar engine handles
// fine-grained per-resource policy at handler-time; this is just a gate so
// the user can't even get an admin-aud JWT minted.
func (h *Handler) assertAudienceAllowed(u authstore.User, audience string) error {
	switch audience {
	case auth.AudienceData, auth.AudienceIAM:
		return nil
	case auth.AudienceAdmin:
		// Any role on the principal *might* permit some admin operation —
		// Cedar makes the actual call. We allow audience escalation when the
		// user holds at least one admin-tier role, and reject pure
		// `tenant.user` callers from receiving admin-aud tokens entirely.
		for _, role := range u.Roles {
			if isAdminRole(role) {
				return nil
			}
		}
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("insufficient role for paladin-admin audience"))
	default:
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unsupported audience %q", audience))
	}
}

// resolveLoginUser picks the user row a Login authenticates against and
// verifies the password. With a tenant hint the lookup is scoped to that
// tenant. Without one the subject may hold a row in SEVERAL tenants (one
// membership per tenant — the model behind SwitchTenant), so the password is
// checked against every candidate (FindBySubjectGlobal caps at 5 rows, so at
// most 5 bcrypt comparisons) and the session lands in the most recently
// used matching tenant; re-scoping afterwards is what the tenant switcher is
// for. This replaced the earlier "multiple tenants — supply X-Tenant-Id"
// InvalidArgument: the login form sends no hint, so a multi-tenant subject
// could never sign in through the UI at all.
// loginOutcome maps a resolveLoginUser error to a bounded metric label. It
// reads the Connect code rather than the message on purpose: the messages are
// deliberately identical across several failures so nothing leaks whether a
// subject exists, and a metric label must not be the place that distinction
// reappears.
func loginOutcome(err error) string {
	switch connect.CodeOf(err) {
	case connect.CodeUnauthenticated:
		return "invalid_credentials"
	case connect.CodeInvalidArgument:
		return "invalid_argument"
	default:
		return "error"
	}
}

func (h *Handler) resolveLoginUser(ctx context.Context, in LoginInput) (authstore.User, error) {
	if in.TenantHint != uuid.Nil {
		u, err := h.users.GetBySubject(ctx, in.TenantHint, in.Subject)
		if err != nil {
			if errors.Is(err, authstore.ErrNotFound) {
				// Same generic message — do not leak whether subject exists.
				return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
			}
			return authstore.User{}, connect.NewError(connect.CodeInternal, err)
		}
		return checkLoginRow(u, in.Password)
	}
	matches, err := h.users.FindBySubjectGlobal(ctx, in.Subject)
	if err != nil {
		return authstore.User{}, connect.NewError(connect.CodeInternal, err)
	}
	switch len(matches) {
	case 0:
		return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	case 1:
		// Single membership keeps the precise error taxonomy (disabled /
		// federated) — nothing to disambiguate, so nothing leaks.
		return checkLoginRow(matches[0], in.Password)
	}
	// Multi-tenant subject: the password picks the memberships it actually
	// opens; the most recently used one wins. Disabled / federated rows are
	// silently skipped here (unlike the single-row path) — with several
	// candidates, per-row detail would leak which tenants the subject is in.
	var best *authstore.User
	for i := range matches {
		m := &matches[i]
		if m.Disabled || len(m.PasswordHash) == 0 {
			continue
		}
		if auth.CheckPassword(m.PasswordHash, in.Password) != nil {
			continue
		}
		if best == nil ||
			(m.LastLoginAt != nil && (best.LastLoginAt == nil || m.LastLoginAt.After(*best.LastLoginAt))) {
			best = m
		}
	}
	if best == nil {
		return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}
	return *best, nil
}

// checkLoginRow applies the single-row login checks: disabled, federated
// (no local password), and the bcrypt comparison itself.
func checkLoginRow(u authstore.User, password string) (authstore.User, error) {
	if u.Disabled {
		return authstore.User{}, connect.NewError(connect.CodePermissionDenied, errors.New("user disabled"))
	}
	if len(u.PasswordHash) == 0 {
		return authstore.User{}, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("user has no password (federated)"))
	}
	if err := auth.CheckPassword(u.PasswordHash, password); err != nil {
		return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}
	return u, nil
}

func isAdminRole(r string) bool {
	switch r {
	case "platform.admin", "tenant.admin", "bucket.admin", "iam.admin":
		return true
	}
	return strings.HasSuffix(r, ".admin")
}

// mintPair issues an access+refresh pair. familyID groups the refresh chain:
// pass uuid.Nil for a fresh login (a new family is generated) or the previous
// token's family on rotation so the chain stays linked for reuse-detection.
func (h *Handler) mintPair(ctx context.Context, u authstore.User, audience string, familyID uuid.UUID) (
	access string, refresh string, accessExp, refreshExp time.Time, err error,
) {
	if familyID == uuid.Nil {
		familyID = uuid.Must(uuid.NewV7())
	}
	// Resolve tenant slug if a lookup is configured. Failure is non-fatal
	// — the access token can still be minted with UUID-only tenant binding,
	// and Cedar policies fall back to UUID-keyed Tenant UIDs. Logging the
	// failure is the caller's responsibility (the minting RPC has access
	// to a logger; this helper does not).
	var slug string
	if h.tenantSlug != nil && u.TenantID != uuid.Nil {
		slug, _ = h.tenantSlug(ctx, u.TenantID)
	}
	access, accessExp, err = h.issuer.MintAccess(issuer.AccessClaims{
		Subject:    u.UserID.String(),
		TenantID:   u.TenantID,
		TenantSlug: slug,
		Audience:   audience,
		Roles:      u.Roles,
		Scopes:     u.Scopes,
		Kind:       auth.PrincipalKindUser,
	})
	if err != nil {
		return "", "", time.Time{}, time.Time{}, fmt.Errorf("mint access: %w", err)
	}

	tokenID := uuid.Must(uuid.NewV7())
	refresh, refreshExp, err = h.issuer.MintRefresh(issuer.RefreshClaims{
		Subject:  u.UserID.String(),
		TenantID: u.TenantID,
		UserID:   u.UserID,
		TokenID:  tokenID,
	})
	if err != nil {
		return "", "", time.Time{}, time.Time{}, fmt.Errorf("mint refresh: %w", err)
	}
	if err := h.refresh.Insert(auth.WithActingTenant(ctx, u.TenantID), authstore.RefreshToken{
		JTI:       tokenID,
		FamilyID:  familyID,
		UserID:    u.UserID,
		TenantID:  u.TenantID,
		IssuedAt:  h.now(),
		ExpiresAt: refreshExp,
	}); err != nil {
		return "", "", time.Time{}, time.Time{}, fmt.Errorf("persist refresh: %w", err)
	}
	return access, refresh, accessExp, refreshExp, nil
}

// parseRefresh delegates to the injected RefreshTokenDecoder.
// onRefreshReplayed records a spent refresh token presented to a
// NON-consuming RPC and leaves the family alone.
//
// ExchangeAudience mints an access token without touching the chain, so a
// token that a concurrent rotation spent a millisecond earlier arrives here
// routinely: the console's BFF fires /me and the audience exchanges together
// carrying one cookie, because the browser cannot update it between requests
// in flight. Treating that as theft revoked the live successor too, and the
// operator was signed out by a race they could not have avoided — observed
// under the e2e suite roughly once per full run.
//
// Declining to revoke costs no containment. The presented token is already
// spent and mints nothing; an attacker holding it gains exactly what the
// legitimate caller gets here, which is a rejection. What reuse detection
// genuinely buys on this path is the SIGNAL, so the audit row stays and says
// plainly that nothing was revoked. Containment stays where the RFC puts it:
// on rotation, which is the call an attacker must make to get a usable token.
func (h *Handler) onRefreshReplayed(ctx context.Context, jti, userID, tenantID uuid.UUID) {
	logger.FromContext(ctx).Warn("spent refresh token presented to a non-consuming RPC; rejected, family left intact",
		zap.String("user_id", userID.String()),
		zap.String("jti", jti.String()))
	if h.audit == nil {
		return
	}
	_ = h.audit.Insert(ctx, admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            h.now().UTC(),
		ActorSubject:  userID.String(),
		ActorTenantID: tenantID,
		ActorAudience: auth.AudienceIAM,
		Action:        "iam.RefreshTokenReplayed",
		ResourceName:  "users/" + userID.String(),
		ErrorMessage:  "spent refresh token presented to ExchangeAudience; rejected without revoking the family",
	})
}

// supersessionGrace is how long after a rotation the superseded token is still
// honoured by the NON-CONSUMING paths.
//
// Sized to the thing it covers: two requests a browser dispatched together,
// where the second carries the cookie the first is in the middle of replacing.
// That gap is milliseconds; seconds of tolerance is already generous. It is
// deliberately far shorter than the token's lifetime — this is a window for a
// race, not a second validity period.
//
// What it costs: inside the window, a stolen token that was ALREADY rotated
// can still mint an access token. It cannot rotate, cannot extend itself, and
// the window closes on its own. That is the standard replay allowance in
// rotating-refresh-token schemes, and it is the price of not keeping the same
// state in every client that talks to this API.
const supersessionGrace = 30 * time.Second

// errRotatedBySibling is what a caller gets when its refresh token was traded
// in by a concurrent request carrying the same cookie — two tabs opened
// together, each running the console's session bootstrap.
//
// Aborted, not Unauthenticated, and the distinction is the whole point. The
// session is fine; this particular request lost a race and can retry
// differently. A client that reads Unauthenticated here signs the operator
// out of a working session, which is exactly the bug this replaced.
//
// It is deliberately NOT a rotation. The loser does not get a refresh token —
// it gets told to ask ExchangeAudience instead, which mints an access token
// without touching the chain and already tolerates a just-superseded token.
// So a stolen token that lost its race still cannot obtain one, and reuse
// detection on this path stays exactly as strict as it was.
var errRotatedBySibling = connect.NewError(connect.CodeAborted,
	errors.New("refresh token was rotated by a concurrent request; derive an access token with ExchangeAudience instead"))

// onRefreshRaceLost records a lost rotation race. Info, not Warn: this is an
// expected outcome of two tabs bootstrapping together, and logging it as a
// warning taught operators to ignore the log line that also reports theft.
//
// No audit event and no family revocation — nothing suspicious happened, and
// the family is healthy by construction, since the winner is holding its new
// head.
func (h *Handler) onRefreshRaceLost(ctx context.Context, jti, userID uuid.UUID) {
	logger.FromContext(ctx).Info("refresh token rotated by a concurrent request; caller told to exchange instead",
		zap.String("user_id", userID.String()),
		zap.String("jti", jti.String()))
}

// withinSupersessionGrace reports whether a revoked token was revoked BY
// ROTATION and recently enough to still be honoured.
//
// Both halves are load-bearing, and the first carries more weight than it
// looks. superseded_at is written by rotation and by nothing else — and it is
// CLEARED again when the family is deliberately killed, by logout or by reuse
// detection (see RevokeRefreshTokenFamily). Without that clearing the stamp
// was write-once, so a predecessor stayed "recently superseded" after its
// session had ended and this window kept honouring it for the rest of its
// thirty seconds. Verified on a live stack rather than reasoned about: log in,
// load a page (which rotates), log out, present the pre-rotation token — 200.
//
// Asking the family whether it still had a live member would have been the
// wrong question: rotation supersedes the old token and inserts the new one in
// two statements, so there is an instant where a family legitimately has none,
// and a concurrent sibling would be refused for a race rather than a
// revocation. Tried, and it broke the two-tab case it was meant to protect.
func (h *Handler) withinSupersessionGrace(t authstore.RefreshToken) bool {
	if t.SupersededAt == nil {
		return false
	}
	if h.now().After(t.ExpiresAt) {
		return false
	}
	return h.now().Sub(*t.SupersededAt) <= supersessionGrace
}

// onRefreshReuse revokes the replayed token's FAMILY (RFC 6819 reuse
// detection — not every session the user has; RevokeRefreshTokenFamily is
// scoped to the compromised chain), logs a warning, and writes an audit row
// (is_error → highlighted in the admin audit console). Best-effort — never
// alters the caller's already decided rejection.
//
// Reserved for the CONSUMING path. Rotation is where presenting a spent token
// is evidence rather than coincidence: the legitimate holder can only have one
// live token, so a second presentation means two holders. Reaching for this on
// a read-only path costs real sessions and buys nothing — see
// onRefreshReplayed.
func (h *Handler) onRefreshReuse(ctx context.Context, jti, userID, tenantID uuid.UUID) {
	revoked, err := h.refresh.RevokeFamilyOf(auth.WithActingTenant(ctx, tenantID), jti)
	logger.FromContext(ctx).Warn("refresh token reuse detected; revoked the token family",
		zap.String("user_id", userID.String()),
		zap.Int64("revoked", revoked),
		zap.Error(err))
	if h.audit == nil {
		return
	}
	_ = h.audit.Insert(ctx, admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            h.now().UTC(),
		ActorSubject:  userID.String(),
		ActorTenantID: tenantID,
		ActorAudience: auth.AudienceIAM,
		Action:        "iam.RefreshTokenReuseDetected",
		ResourceName:  "users/" + userID.String(),
		ErrorMessage:  "rotated refresh token replayed; token family revoked",
	})
}

func (h *Handler) parseRefresh(token string) (uuid.UUID, uuid.UUID, uuid.UUID, error) {
	if h.refreshDecoder == nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("refresh decoder not configured")
	}
	return h.refreshDecoder.DecodeRefresh(token)
}

// ─── Membership paging ──────────────────────────────────────────────────────

// membershipDefaultPageSize is deliberately larger than the 50 other List RPCs
// default to. This endpoint backs the tenant switcher, and a membership the
// switcher does not show is a tenant the caller cannot reach — truncating it
// hides access rather than trimming a display. The page bounds the
// pathological account; it is not meant to be hit by an ordinary one.
const membershipDefaultPageSize = 500

// ListMembershipsInput carries the page controls for ListMyMemberships.
type ListMembershipsInput struct {
	PageSize  int32
	PageToken string
}

// Membership cursors are "<unix_nanos>:<uuid>" — the (created_at, id) tuple
// the query orders by. Opaque to the caller by contract; readable here because
// a malformed cursor should produce InvalidArgument, not a silent first page.
func encodeMembershipCursor(created time.Time, id uuid.UUID) string {
	return strconv.FormatInt(created.UnixNano(), 10) + ":" + id.String()
}

func decodeMembershipCursor(tok string) (time.Time, uuid.UUID, error) {
	if tok == "" {
		// Zero time sorts before every row; uuid.Nil is the lowest uuid.
		return time.Time{}, uuid.Nil, nil
	}
	nanos, idStr, ok := strings.Cut(tok, ":")
	if !ok {
		return time.Time{}, uuid.Nil, fmt.Errorf("malformed page_token %q", tok)
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("malformed page_token %q: %w", tok, err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("malformed page_token %q: %w", tok, err)
	}
	return time.Unix(0, n).UTC(), id, nil
}
