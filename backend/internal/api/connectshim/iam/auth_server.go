package iam

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	commonpb "github.com/oleg-tkachuk/paladin/internal/api/pb/common/v1"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// AuthServer bridges generated Connect handlers to authh.Handler.
type AuthServer struct {
	paladiniamv1connect.UnimplementedAuthServiceHandler
	H *authh.Handler
}

func NewAuthServer(h *authh.Handler) *AuthServer { return &AuthServer{H: h} }

func (s *AuthServer) Login(ctx context.Context, req *connect.Request[pb.LoginRequest]) (*connect.Response[pb.LoginResponse], error) {
	m := req.Msg
	var tenant uuid.UUID
	// Tenant hint comes via header "X-Tenant-Id" (set by frontend / proxy).
	if h := req.Header().Get("X-Tenant-Id"); h != "" {
		if id, err := uuid.Parse(h); err == nil {
			tenant = id
		}
	}
	out, err := s.H.Login(ctx, authh.LoginInput{
		Subject:           m.GetSubject(),
		Password:          m.GetPassword(),
		UpstreamCode:      m.GetUpstreamCode(),
		RequestedAudience: m.GetRequestedAudience(),
		TenantHint:        tenant,
	})
	if err != nil {
		return nil, err
	}
	// Relative TTLs from "now". The previous form used
	// `AccessExpiresAt.Sub(out.User.CreatedAt)` which happens to look
	// right for users created near login time but balloons for accounts
	// minted hours / days earlier.
	now := time.Now()
	return connect.NewResponse(&pb.LoginResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(now).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(now).Seconds()),
			TokenType:               "Bearer",
			Audience:                out.Audience,
		},
		User: userToProto(&out.User),
	}), nil
}

func (s *AuthServer) RefreshToken(ctx context.Context, req *connect.Request[pb.RefreshTokenRequest]) (*connect.Response[pb.RefreshTokenResponse], error) {
	out, err := s.H.RefreshToken(ctx, authh.RefreshInput{
		RefreshToken:      req.Msg.GetRefreshToken(),
		RequestedAudience: req.Msg.GetRequestedAudience(),
	})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return connect.NewResponse(&pb.RefreshTokenResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(now).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(now).Seconds()),
			TokenType:               "Bearer",
			Audience:                out.Audience,
		},
	}), nil
}

func (s *AuthServer) Revoke(ctx context.Context, req *connect.Request[pb.RevokeRequest]) (*connect.Response[pb.RevokeResponse], error) {
	if err := s.H.Revoke(ctx, req.Msg.GetToken()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RevokeResponse{}), nil
}

func (s *AuthServer) WhoAmI(ctx context.Context, req *connect.Request[pb.WhoAmIRequest]) (*connect.Response[pb.WhoAmIResponse], error) {
	out, err := s.H.WhoAmI(ctx, req.Msg.GetRoutePageToken())
	if err != nil {
		return nil, err
	}
	// tenant_slug is sourced from the JWT principal claim (minted by
	// the issuer with the value of tenants.slug at login time). The
	// SPA uses it to render slug-form tenant URLs without a
	// follow-up GetTenant lookup. PrincipalFromContext can fail in
	// edge cases (token without principal — shouldn't happen post-
	// authn-middleware), so we surface empty rather than 5xx.
	var tenantSlug string
	if p, perr := auth.PrincipalFromContext(ctx); perr == nil {
		tenantSlug = p.TenantSlug
	}
	return connect.NewResponse(&pb.WhoAmIResponse{
		User:            userToProto(&out.User),
		Audience:        out.Audience,
		TenantSlug:      tenantSlug,
		Routes:          collectionRoutesToProto(out.Routes),
		RoutesTruncated: out.RoutesTruncated,
		NextPageToken:   out.NextPageToken,
	}), nil
}

// collectionRoutesToProto maps the handler's route table (ADR-0010 Phase 4)
// onto the wire message. nil/empty in → nil out (omitted field).
func collectionRoutesToProto(routes []authh.CollectionRoute) []*pb.CollectionRoute {
	if len(routes) == 0 {
		return nil
	}
	out := make([]*pb.CollectionRoute, len(routes))
	for i, r := range routes {
		out[i] = &pb.CollectionRoute{
			Canonical:  r.Canonical,
			TenantPath: r.TenantPath,
			BareAlias:  r.BareAlias,
			Backend:    r.Backend,
			Bucket:     r.Bucket,
		}
	}
	return out
}

func (s *AuthServer) ChangePassword(ctx context.Context, req *connect.Request[pb.ChangePasswordRequest]) (*connect.Response[pb.ChangePasswordResponse], error) {
	if err := s.H.ChangePassword(ctx, req.Msg.GetOldPassword(), req.Msg.GetNewPassword()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ChangePasswordResponse{}), nil
}

func (s *AuthServer) ExchangeAudience(ctx context.Context, req *connect.Request[pb.ExchangeAudienceRequest]) (*connect.Response[pb.ExchangeAudienceResponse], error) {
	out, err := s.H.ExchangeAudience(ctx, authh.ExchangeAudienceInput{
		RefreshToken:   req.Msg.GetRefreshToken(),
		TargetAudience: req.Msg.GetTargetAudience(),
	})
	if err != nil {
		return nil, err
	}
	// access_expires_in_seconds: relative TTL, like Login/RefreshToken.
	ttl := int32(time.Until(out.AccessExpiresAt).Seconds())
	return connect.NewResponse(&pb.ExchangeAudienceResponse{
		AccessToken:            out.AccessToken,
		AccessExpiresInSeconds: ttl,
		TokenType:              "Bearer",
	}), nil
}

func (s *AuthServer) ListMyMemberships(ctx context.Context, req *connect.Request[pb.ListMyMembershipsRequest]) (*connect.Response[pb.ListMyMembershipsResponse], error) {
	ms, next, err := s.H.ListMyMemberships(ctx, authh.ListMembershipsInput{
		PageSize:  req.Msg.GetPage().GetPageSize(),
		PageToken: req.Msg.GetPage().GetPageToken(),
	})
	if err != nil {
		return nil, err
	}
	out := make([]*pb.Membership, len(ms))
	for i, m := range ms {
		out[i] = &pb.Membership{
			TenantId:   m.TenantID.String(),
			TenantSlug: m.TenantSlug,
			Roles:      m.Roles,
			Disabled:   m.Disabled,
			Current:    m.Current,
		}
	}
	resp := &pb.ListMyMembershipsResponse{Memberships: out}
	if next != "" {
		resp.Page = &commonpb.PageResponse{NextPageToken: next}
	}
	return connect.NewResponse(resp), nil
}

func (s *AuthServer) SwitchTenant(ctx context.Context, req *connect.Request[pb.SwitchTenantRequest]) (*connect.Response[pb.SwitchTenantResponse], error) {
	target, err := uuid.Parse(req.Msg.GetTargetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.SwitchTenant(ctx, target, req.Msg.GetRequestedAudience())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return connect.NewResponse(&pb.SwitchTenantResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(now).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(now).Seconds()),
			TokenType:               "Bearer",
			Audience:                out.Audience,
		},
		User: userToProto(&out.User),
	}), nil
}

var _ paladiniamv1connect.AuthServiceHandler = (*AuthServer)(nil)
