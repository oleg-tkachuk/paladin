package iam

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/authh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// AuthServer bridges generated Connect handlers to authh.Handler.
type AuthServer struct {
	paladiniamv1connect.UnimplementedAuthServiceHandler
	H authHandler
}

func NewAuthServer(h *authh.Handler) *AuthServer { return &AuthServer{H: h} }

func (s *AuthServer) Login(ctx context.Context, req *pb.LoginRequest) (*pb.LoginResponse, error) {
	info := unary.Info(ctx)
	m := req
	var tenant uuid.UUID
	// Tenant hint comes via header "X-Tenant-Id" (set by frontend / proxy).
	if h := info.RequestHeader().Get("X-Tenant-Id"); h != "" {
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
	return &pb.LoginResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(now).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(now).Seconds()),
			TokenType:               "Bearer",
			Audience:                out.Audience,
		},
		User: userToProto(&out.User),
	}, nil
}

func (s *AuthServer) RefreshToken(ctx context.Context, req *pb.RefreshTokenRequest) (*pb.RefreshTokenResponse, error) {
	out, err := s.H.RefreshToken(ctx, authh.RefreshInput{
		RefreshToken:      req.GetRefreshToken(),
		RequestedAudience: req.GetRequestedAudience(),
	})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &pb.RefreshTokenResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(now).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(now).Seconds()),
			TokenType:               "Bearer",
			Audience:                out.Audience,
		},
	}, nil
}

func (s *AuthServer) Revoke(ctx context.Context, req *pb.RevokeRequest) (*pb.RevokeResponse, error) {
	if err := s.H.Revoke(ctx, req.GetToken()); err != nil {
		return nil, err
	}
	return &pb.RevokeResponse{}, nil
}

func (s *AuthServer) WhoAmI(ctx context.Context, req *pb.WhoAmIRequest) (*pb.WhoAmIResponse, error) {
	out, err := s.H.WhoAmI(ctx, req.GetRoutePageToken())
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
	return &pb.WhoAmIResponse{
		User:            userToProto(&out.User),
		Audience:        out.Audience,
		TenantSlug:      tenantSlug,
		Routes:          collectionRoutesToProto(out.Routes),
		RoutesTruncated: out.RoutesTruncated,
		NextPageToken:   out.NextPageToken,
	}, nil
}

// collectionRoutesToProto maps the handler's route table (ADR-0014 Phase 4)
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

func (s *AuthServer) ChangePassword(ctx context.Context, req *pb.ChangePasswordRequest) (*pb.ChangePasswordResponse, error) {
	if err := s.H.ChangePassword(ctx, req.GetOldPassword(), req.GetNewPassword()); err != nil {
		return nil, err
	}
	return &pb.ChangePasswordResponse{}, nil
}

func (s *AuthServer) ExchangeAudience(ctx context.Context, req *pb.ExchangeAudienceRequest) (*pb.ExchangeAudienceResponse, error) {
	out, err := s.H.ExchangeAudience(ctx, authh.ExchangeAudienceInput{
		RefreshToken:   req.GetRefreshToken(),
		TargetAudience: req.GetTargetAudience(),
	})
	if err != nil {
		return nil, err
	}
	// access_expires_in_seconds: relative TTL, like Login/RefreshToken.
	ttl := int32(time.Until(out.AccessExpiresAt).Seconds())
	return &pb.ExchangeAudienceResponse{
		AccessToken:            out.AccessToken,
		AccessExpiresInSeconds: ttl,
		TokenType:              "Bearer",
	}, nil
}

func (s *AuthServer) ListMyMemberships(ctx context.Context, req *pb.ListMyMembershipsRequest) (*pb.ListMyMembershipsResponse, error) {
	ms, next, err := s.H.ListMyMemberships(ctx, authh.ListMembershipsInput{
		PageSize:  req.GetPage().GetPageSize(),
		PageToken: req.GetPage().GetPageToken(),
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
	return resp, nil
}

func (s *AuthServer) SwitchTenant(ctx context.Context, req *pb.SwitchTenantRequest) (*pb.SwitchTenantResponse, error) {
	target, err := uuid.Parse(req.GetTargetTenantId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	out, err := s.H.SwitchTenant(ctx, target, req.GetRequestedAudience())
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &pb.SwitchTenantResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(now).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(now).Seconds()),
			TokenType:               "Bearer",
			Audience:                out.Audience,
		},
		User: userToProto(&out.User),
	}, nil
}

var _ paladiniamv1connect.AuthServiceHandler = (*AuthServer)(nil)
