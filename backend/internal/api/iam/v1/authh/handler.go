// Package authh implements the IAM AuthService — login, refresh, who-am-I,
// password change. The handler is plane-agnostic Go (no Connect imports);
// connectshim/iam wraps it for the wire layer.
package authh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/issuer"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
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

type Handler struct {
	users          authstore.UserRepository
	refresh        authstore.RefreshTokenRepository
	issuer         tokenMinter
	refreshDecoder RefreshTokenDecoder
	policy         cedar.Authorizer
	tenantSlug     TenantSlugLookup
	now            func() time.Time

	// Optional: refresh-token reuse-detection observability.
	audit reuseAuditor
	log   *zap.Logger
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
}

func (h *Handler) Login(ctx context.Context, in LoginInput) (*LoginOutput, error) {
	if in.Subject == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subject required"))
	}
	if in.Password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("password required"))
	}

	u, err := h.resolveUser(ctx, in.Subject, in.TenantHint)
	if err != nil {
		return nil, err
	}
	if u.Disabled {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("user disabled"))
	}
	if len(u.PasswordHash) == 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("user has no password (federated)"))
	}
	if err := auth.CheckPassword(u.PasswordHash, in.Password); err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	}

	audience := in.RequestedAudience
	if audience == "" {
		audience = auth.AudienceData
	}
	if err := h.assertAudienceAllowed(u, audience); err != nil {
		return nil, err
	}

	access, refresh, accessExp, refreshExp, err := h.mintPair(ctx, u, audience, uuid.Nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if err := h.users.TouchLogin(ctx, u.UserID, h.now()); err != nil {
		// Non-fatal — login succeeded.
		_ = err
	}

	return &LoginOutput{
		User:             u,
		AccessToken:      access,
		RefreshToken:     refresh,
		AccessExpiresAt:  accessExp,
		RefreshExpiresAt: refreshExp,
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
		// the whole family and record it before rejecting.
		if errors.Is(err, authstore.ErrTokenRevoked) {
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

	// Rotation: revoke the presented refresh, mint a fresh pair in the SAME
	// family so the chain stays linked for reuse-detection.
	if err := h.refresh.Revoke(ctx, jti); err != nil {
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
		// Replay of a rotated (revoked) token → RFC 6819 theft signal: revoke
		// the whole family and record it before rejecting.
		if errors.Is(err, authstore.ErrTokenRevoked) {
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
	jti, _, _, err := h.parseRefresh(token)
	if err == nil {
		// It's a refresh token — revoke by JTI.
		_ = h.refresh.Revoke(ctx, jti)
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
}

func (h *Handler) WhoAmI(ctx context.Context) (*WhoAmIOutput, error) {
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
	return &WhoAmIOutput{User: u, Audience: p.Audience}, nil
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
// admin-tier roles cannot mint an paladin-admin token. The Cedar engine handles
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

// resolveUser looks up the user by subject. With a tenant hint, scoped to
// that tenant. Without one, scans across tenants and requires a unique
// match — multiple matches are reported as InvalidArgument so the frontend
// knows to re-prompt for a tenant.
func (h *Handler) resolveUser(ctx context.Context, subject string, hint uuid.UUID) (authstore.User, error) {
	if hint != uuid.Nil {
		u, err := h.users.GetBySubject(ctx, hint, subject)
		if err != nil {
			if errors.Is(err, authstore.ErrNotFound) {
				return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
			}
			return authstore.User{}, connect.NewError(connect.CodeInternal, err)
		}
		return u, nil
	}
	matches, err := h.users.FindBySubjectGlobal(ctx, subject)
	if err != nil {
		return authstore.User{}, connect.NewError(connect.CodeInternal, err)
	}
	switch len(matches) {
	case 0:
		// Same generic message — do not leak whether subject exists.
		return authstore.User{}, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid credentials"))
	case 1:
		return matches[0], nil
	}
	return authstore.User{}, connect.NewError(connect.CodeInvalidArgument,
		errors.New("subject is registered in multiple tenants — supply X-Tenant-Id"))
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
	if err := h.refresh.Insert(ctx, authstore.RefreshToken{
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
// onRefreshReuse revokes every refresh token for the user (RFC 6819 reuse
// detection), logs a warning, and writes an audit row (is_error → highlighted
// in the admin audit console). Best-effort — never alters the caller's already
// decided rejection.
func (h *Handler) onRefreshReuse(ctx context.Context, jti, userID, tenantID uuid.UUID) {
	revoked, err := h.refresh.RevokeFamilyOf(ctx, jti)
	if h.log != nil {
		h.log.Warn("refresh token reuse detected; revoked the token family",
			zap.String("user_id", userID.String()),
			zap.Int64("revoked", revoked),
			zap.Error(err))
	}
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
