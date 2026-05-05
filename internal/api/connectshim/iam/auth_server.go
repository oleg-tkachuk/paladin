package iam

import (
	"context"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
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
	return connect.NewResponse(&pb.LoginResponse{
		Tokens: &pb.TokenPair{
			AccessToken:             out.AccessToken,
			AccessExpiresInSeconds:  int32(out.AccessExpiresAt.Sub(out.User.CreatedAt).Seconds()),
			RefreshToken:            out.RefreshToken,
			RefreshExpiresInSeconds: int32(out.RefreshExpiresAt.Sub(out.User.CreatedAt).Seconds()),
			TokenType:               "Bearer",
		},
		User: userToProto(&out.User),
	}), nil
}

func (s *AuthServer) RefreshToken(ctx context.Context, req *connect.Request[pb.RefreshTokenRequest]) (*connect.Response[pb.RefreshTokenResponse], error) {
	out, err := s.H.RefreshToken(ctx, authh.RefreshInput{
		RefreshToken: req.Msg.GetRefreshToken(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RefreshTokenResponse{
		Tokens: &pb.TokenPair{
			AccessToken:  out.AccessToken,
			RefreshToken: out.RefreshToken,
			TokenType:    "Bearer",
		},
	}), nil
}

func (s *AuthServer) Revoke(ctx context.Context, req *connect.Request[pb.RevokeRequest]) (*connect.Response[pb.RevokeResponse], error) {
	if err := s.H.Revoke(ctx, req.Msg.GetToken()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RevokeResponse{}), nil
}

func (s *AuthServer) WhoAmI(ctx context.Context, _ *connect.Request[pb.WhoAmIRequest]) (*connect.Response[pb.WhoAmIResponse], error) {
	out, err := s.H.WhoAmI(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.WhoAmIResponse{
		User:     userToProto(&out.User),
		Audience: out.Audience,
	}), nil
}

func (s *AuthServer) ChangePassword(ctx context.Context, req *connect.Request[pb.ChangePasswordRequest]) (*connect.Response[pb.ChangePasswordResponse], error) {
	if err := s.H.ChangePassword(ctx, req.Msg.GetOldPassword(), req.Msg.GetNewPassword()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ChangePasswordResponse{}), nil
}

var _ paladiniamv1connect.AuthServiceHandler = (*AuthServer)(nil)
