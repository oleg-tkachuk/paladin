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
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// Handler wires the dependencies the RPCs need.
type Handler struct {
	issuer *api_token.Issuer
	store  api_token.Store
	policy cedar.Authorizer
}

// NewHandler builds the Handler. issuer / store / policy are required;
// passing nil panics — wiring bugs should fail loud at boot.
func NewHandler(issuer *api_token.Issuer, store api_token.Store, policy cedar.Authorizer) *Handler {
	if issuer == nil || store == nil || policy == nil {
		panic("apitokenh: issuer / store / policy are required")
	}
	return &Handler{issuer: issuer, store: store, policy: policy}
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
		&cedar.Principal{
			Subject:    p.Subject,
			TenantID:   p.TenantID,
			TenantSlug: p.TenantSlug,
			Roles:      p.Roles,
			Scopes:     apiutil.ScopeStrings(p.Scopes),
		},
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

	tenantID, err := uuid.Parse(req.Msg.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
	}
	ttl := time.Duration(req.Msg.GetTtlSeconds()) * time.Second

	tok, err := h.issuer.Issue(ctx, api_token.IssueRequest{
		TenantID:  tenantID,
		Name:      req.Msg.GetName(),
		Scopes:    req.Msg.GetScopes(),
		Audience:  req.Msg.GetAudience(),
		TTL:       ttl,
		CreatedBy: caller.Subject,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	return connect.NewResponse(&adminv1.APITokenServiceCreateResponse{
		ApiToken: tokenToProto(*tok),
		Token:    tok.Plaintext,
	}), nil
}

// Revoke marks an API token as revoked. Idempotent.
func (h *Handler) Revoke(ctx context.Context, req *connect.Request[adminv1.APITokenServiceRevokeRequest]) (*connect.Response[adminv1.APITokenServiceRevokeResponse], error) {
	if _, err := h.authorize(ctx, "revoke"); err != nil {
		return nil, err
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("id: %w", err))
	}
	if err := h.store.Revoke(ctx, id); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&adminv1.APITokenServiceRevokeResponse{}), nil
}

// List enumerates API tokens for a tenant. Token plaintext / hash are
// never returned; callers see metadata only.
func (h *Handler) List(ctx context.Context, req *connect.Request[adminv1.APITokenServiceListRequest]) (*connect.Response[adminv1.APITokenServiceListResponse], error) {
	if _, err := h.authorize(ctx, "list"); err != nil {
		return nil, err
	}
	tenantID, err := uuid.Parse(req.Msg.GetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("tenant_id: %w", err))
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

// tokenToProto maps the in-process Token to the proto shape. Plaintext
// is intentionally never copied — the proto field is populated on Create
// from the Issuer's return, not from this converter.
func tokenToProto(t api_token.Token) *adminv1.APIToken {
	out := &adminv1.APIToken{
		Id:        t.ID.String(),
		TenantId:  t.TenantID.String(),
		Name:      t.Name,
		Prefix:    t.Prefix,
		Scopes:    t.Scopes,
		Audience:  t.Audience,
		ExpiresAt: timestamppb.New(t.ExpiresAt),
		CreatedAt: timestamppb.New(t.CreatedAt),
		CreatedBy: t.CreatedBy,
	}
	if t.RevokedAt != nil {
		out.RevokedAt = timestamppb.New(*t.RevokedAt)
	}
	if t.LastUsedAt != nil {
		out.LastUsedAt = timestamppb.New(*t.LastUsedAt)
	}
	return out
}
