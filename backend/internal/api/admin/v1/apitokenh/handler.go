// Package apitokenh implements the admin APITokenService — Create /
// Revoke / List / GetSelf for hashed-bearer M2M tokens. Wraps
// internal/auth/api_token (the in-process types + Issuer + Store) into
// a Connect handler suitable for mux registration.
//
// Authorisation: every mutating RPC currently requires platform.admin
// (gated via Cedar). GetSelf is open to anyone presenting a valid API
// token — it just reads the context value the interceptor stamped.
//
// The plaintext token is returned exactly once on Create. The List
// path never returns it; callers see metadata only.
package apitokenh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/oleg-tkachuk/paladin/backend/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// tokenIssuer is the narrow slice of *api_token.Issuer the Create RPC needs.
// Declared as an interface so the handler is unit-testable with a stub; the
// concrete *api_token.Issuer satisfies it implicitly, so call sites are
// unchanged.
type tokenIssuer interface {
	Issue(ctx context.Context, req api_token.IssueRequest) (*api_token.Token, error)
}

// Handler wires the dependencies the RPCs need.
type Handler struct {
	issuer  tokenIssuer
	store   api_token.Store
	limiter ratelimit.Limiter
	policy  cedar.Authorizer
}

// NewHandler builds the Handler. issuer / store / limiter / policy are
// required; passing nil panics — wiring bugs should fail loud at boot.
//
// limiter must be non-nil even on deploys that don't use rate limits;
// pass ratelimit.NoopLimiter{} in that case so GetUsage degrades to an
// empty snapshot rather than a runtime nil-deref.
func NewHandler(
	issuer tokenIssuer,
	store api_token.Store,
	limiter ratelimit.Limiter,
	policy cedar.Authorizer,
) *Handler {
	if issuer == nil || store == nil || limiter == nil || policy == nil {
		panic("apitokenh: issuer / store / limiter / policy are required")
	}
	return &Handler{issuer: issuer, store: store, limiter: limiter, policy: policy}
}

// authorize gates an RPC against Cedar under the resource kind
// `api_token`. Mirrors capabilityh's pattern; admin actions live in
// `api_token:create / revoke / list`.
func (h *Handler) authorize(ctx context.Context, action string) (*auth.Principal, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(p),
		"api_token:"+action,
		&cedar.Resource{TenantID: p.TenantID, TenantSlug: p.TenantSlug},
		cedar.RequestContext{},
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if decision != cedar.DecisionAllow {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("api_token %s denied", action))
	}
	return p, nil
}

// Create mints an API token and returns the plaintext exactly once.
func (h *Handler) Create(ctx context.Context, req *connect.Request[adminv1.APITokenServiceCreateRequest]) (*connect.Response[adminv1.APITokenServiceCreateResponse], error) {
	caller, err := h.authorize(ctx, "create")
	if err != nil {
		return nil, err
	}

	tenantID, err := parseTenantParent(req.Msg.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Minting for somebody else's tenant is a platform operation.
	//
	// This check is the gate, and it has to live here: the store now sets the
	// RLS tenant from the token being written (it must, or provisioning a new
	// tenant's first credential would be impossible through this RPC), so the
	// database no longer refuses a cross-tenant write on its own. Cedar
	// authorises `api_token:create` against the CALLER's tenant, which says
	// nothing about the tenant named in the request — so without this, a
	// tenant-level admin allowed to mint their own tokens could mint one for
	// any tenant whose id they can guess.
	if tenantID != caller.TenantID && !caller.HasRole(apiutil.RolePlatformAdmin) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("minting an api_token for another tenant requires platform.admin"))
	}

	// Validate every requested scope parses as an auth.Scope. Minting a token
	// whose scope string can't parse is a mistake we reject at the edge: a
	// malformed scope on the data plane fails the request fail-closed (see
	// principalFromAPIToken), so an operator would otherwise mint a token that
	// is dead on arrival. This also pins the scope vocabulary to the resource-
	// scoping set (tenant:/backend:/bucket:/collection:/*).
	for _, s := range req.Msg.GetScopes() {
		if _, perr := auth.ParseScope(s); perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("scope: %w", perr))
		}
	}

	// Granting a role is a platform operation, whoever the token is for.
	//
	// A token that could hand itself a role would make every other gate
	// decorative: mint one with platform.admin and the tenant checks above and
	// on every other admin RPC stop meaning anything. This is deliberately
	// stricter than the cross-tenant check — that one is about WHOSE token this
	// is, this one about WHAT AUTHORITY it carries.
	if len(req.Msg.GetRoles()) > 0 && !caller.HasRole(apiutil.RolePlatformAdmin) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			errors.New("granting roles to an api_token requires platform.admin"))
	}

	ttl := time.Duration(req.Msg.GetTtlSeconds()) * time.Second

	tok, err := h.issuer.Issue(ctx, api_token.IssueRequest{
		TenantID:     tenantID,
		Name:         req.Msg.GetDisplayName(),
		Scopes:       req.Msg.GetScopes(),
		Roles:        req.Msg.GetRoles(),
		Audience:     req.Msg.GetAudience(),
		TTL:          ttl,
		RateLimitRPM: int(req.Msg.GetRateLimitRpm()),
		CreatedBy:    caller.Subject,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&adminv1.APITokenServiceCreateResponse{
		ApiToken: tokenToProto(*tok),
		Token:    tok.Plaintext,
	}), nil
}

// parseTokenName decodes "tenants/{tenant}/apiTokens/{id}".
//
// The tenant segment is the point of the whole conversion. api_tokens carries
// FORCE row-level security keyed on the session tenant, so a handler holding
// only an id cannot scope its query — and cannot learn the tenant either,
// because the row it would read is the one RLS is hiding. Carrying the parent
// in the address breaks that circle without a privileged read path around the
// enforcement mechanism.
func parseTokenName(name string) (tenantID, tokenID uuid.UUID, err error) {
	const (
		tenantPrefix = "tenants/"
		tokenSep     = "/apiTokens/"
	)
	if !strings.HasPrefix(name, tenantPrefix) {
		return uuid.Nil, uuid.Nil, fmt.Errorf("name %q must start with %q", name, tenantPrefix)
	}
	rest := strings.TrimPrefix(name, tenantPrefix)
	tenantPart, idPart, ok := strings.Cut(rest, tokenSep)
	if !ok || tenantPart == "" || idPart == "" {
		return uuid.Nil, uuid.Nil, fmt.Errorf("name %q must be tenants/{tenant}/apiTokens/{id}", name)
	}
	tenantID, err = uuid.Parse(tenantPart)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("tenant in %q: %w", name, err)
	}
	tokenID, err = uuid.Parse(idPart)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("token id in %q: %w", name, err)
	}
	return tenantID, tokenID, nil
}

// parseTenantParent decodes "tenants/{tenant}".
func parseTenantParent(parent string) (uuid.UUID, error) {
	const prefix = "tenants/"
	if !strings.HasPrefix(parent, prefix) {
		return uuid.Nil, fmt.Errorf("parent %q must be tenants/{tenant}", parent)
	}
	id, err := uuid.Parse(strings.TrimPrefix(parent, prefix))
	if err != nil {
		return uuid.Nil, fmt.Errorf("tenant in %q: %w", parent, err)
	}
	return id, nil
}

// scopeToTenant is the guard every tenant-addressed RPC on this service runs:
// refuse the caller who may not cross, then tell RLS about the caller who may.
// Both halves are needed — a permission check alone still leaves the admitted
// platform admin reading an empty page, because the session stays bound to
// their own tenant.
func scopeToTenant(ctx context.Context, caller *auth.Principal, tenantID uuid.UUID, what string) (context.Context, error) {
	if tenantID != caller.TenantID && !caller.HasRole(apiutil.RolePlatformAdmin) {
		return nil, connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("cross-tenant api_token %s denied", what))
	}
	return auth.WithActingTenant(ctx, tenantID), nil
}

// Revoke marks an API token as revoked. Idempotent.
func (h *Handler) Revoke(ctx context.Context, req *connect.Request[adminv1.APITokenServiceRevokeRequest]) (*connect.Response[adminv1.APITokenServiceRevokeResponse], error) {
	caller, err := h.authorize(ctx, "revoke")
	if err != nil {
		return nil, err
	}
	tenantID, id, err := parseTokenName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ctx, err = scopeToTenant(ctx, caller, tenantID, "revoke")
	if err != nil {
		return nil, err
	}
	if err := h.store.Revoke(ctx, id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&adminv1.APITokenServiceRevokeResponse{}), nil
}

// List enumerates API tokens for a tenant. Token plaintext / hash are
// never returned; callers see metadata only.
func (h *Handler) List(ctx context.Context, req *connect.Request[adminv1.APITokenServiceListRequest]) (*connect.Response[adminv1.APITokenServiceListResponse], error) {
	caller, err := h.authorize(ctx, "list")
	if err != nil {
		return nil, err
	}
	tenantID, err := parseTenantParent(req.Msg.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ctx, err = scopeToTenant(ctx, caller, tenantID, "list")
	if err != nil {
		return nil, err
	}
	tokens, next, err := h.store.ListByTenant(ctx, api_token.ListByTenantArgs{
		TenantID:       tenantID,
		IncludeRevoked: req.Msg.GetIncludeRevoked(),
		IncludeExpired: req.Msg.GetIncludeExpired(),
		Cursor:         req.Msg.GetPageToken(),
		Limit:          req.Msg.GetPageSize(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	out := make([]*adminv1.APIToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, tokenToProto(t))
	}
	return connect.NewResponse(&adminv1.APITokenServiceListResponse{
		ApiTokens:     out,
		NextPageToken: next,
	}), nil
}

// GetSelf returns the API token attached to the calling context. NOT_FOUND
// when the request was authenticated by something other than an API token.
// No Cedar gate — the bearer of a token can always introspect what they
// already hold.
func (h *Handler) GetSelf(ctx context.Context, _ *connect.Request[adminv1.APITokenServiceGetSelfRequest]) (*connect.Response[adminv1.APITokenServiceGetSelfResponse], error) {
	tok, ok := auth.APITokenFromContext(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound,
			fmt.Errorf("request not authenticated via API token"))
	}
	return connect.NewResponse(&adminv1.APITokenServiceGetSelfResponse{
		ApiToken: tokenToProto(*tok),
	}), nil
}

// GetUsage returns a readonly rate-limit snapshot for a token. Cedar-
// gated under api_token:read so admin-tier callers can inspect any
// tenant's tokens without a separate per-token authorisation rule.
// Web UI consumes this from token-detail cards; safe to poll.
func (h *Handler) GetUsage(ctx context.Context, req *connect.Request[adminv1.APITokenServiceGetUsageRequest]) (*connect.Response[adminv1.APITokenServiceGetUsageResponse], error) {
	caller, err := h.authorize(ctx, "read")
	if err != nil {
		return nil, err
	}
	tenantID, id, err := parseTokenName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ctx, err = scopeToTenant(ctx, caller, tenantID, "read")
	if err != nil {
		return nil, err
	}
	tok, err := h.store.Get(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	snap, err := h.limiter.Usage(ctx, id)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &adminv1.APITokenServiceGetUsageResponse{
		Name:                req.Msg.GetName(),
		LimitRpm:            safecast.Int32(tok.RateLimitRPM),
		CurrentBucketCount:  snap.CurrentBucketCount,
		PreviousBucketCount: snap.PreviousBucketCount,
		WeightedCount:       snap.WeightedCount,
		WindowResetsAt:      timestamppb.New(snap.WindowResetsAt),
	}
	if tok.LastUsedAt != nil {
		resp.LastUsedAt = timestamppb.New(*tok.LastUsedAt)
	}
	return connect.NewResponse(resp), nil
}

// isNotFound matches sentinel errors that indicate "no row" — keeps the
// callsite tidy and lets handlers branch on a single helper.
func isNotFound(err error) bool {
	return errors.Is(err, api_token.ErrTokenNotFound)
}

// tokenToProto maps the in-process Token to the proto shape. Plaintext
// is intentionally never copied — the proto field is populated on Create
// from the Issuer's return, not from this converter.
func tokenToProto(t api_token.Token) *adminv1.APIToken {
	out := &adminv1.APIToken{
		// The resource name every addressed RPC on this service now takes.
		// Returned so a caller never has to assemble it, and never has to
		// carry a bare id that cannot be scoped.
		Name:         "tenants/" + t.TenantID.String() + "/apiTokens/" + t.ID.String(),
		Id:           t.ID.String(),
		TenantId:     t.TenantID.String(),
		DisplayName:  t.Name,
		Prefix:       t.Prefix,
		Roles:        t.Roles,
		Scopes:       t.Scopes,
		Audience:     t.Audience,
		ExpiresAt:    timestamppb.New(t.ExpiresAt),
		CreatedAt:    timestamppb.New(t.CreatedAt),
		CreatedBy:    t.CreatedBy,
		RateLimitRpm: safecast.Int32(t.RateLimitRPM),
	}
	if t.RevokedAt != nil {
		out.RevokedAt = timestamppb.New(*t.RevokedAt)
	}
	if t.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*t.LastUsedAt)
	}
	return out
}
