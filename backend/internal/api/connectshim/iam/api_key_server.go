package iam

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/apikeyh"
	pb "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"
	authstore "github.com/oleg-tkachuk/paladin/internal/auth/store"
)

type ApiKeyServer struct {
	paladiniamv1connect.UnimplementedApiKeyServiceHandler
	H *apikeyh.Handler
}

func NewApiKeyServer(h *apikeyh.Handler) *ApiKeyServer { return &ApiKeyServer{H: h} }

func (s *ApiKeyServer) CreateApiKey(ctx context.Context, req *connect.Request[pb.CreateApiKeyRequest]) (*connect.Response[pb.CreateApiKeyResponse], error) {
	m := req.Msg
	tenantID, err := tenantFromParent(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.CreateApiKey(ctx, apikeyh.CreateApiKeyInput{
		TenantID:    tenantID,
		Description: m.GetDescription(),
		Roles:       m.GetRoles(),
		Scopes:      scopesFromProto(m.GetScopes()),
		TTL:         m.GetTtl().AsDuration(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.CreateApiKeyResponse{
		ApiKey: apiKeyToProto(&out.ApiKey),
		Secret: out.Secret,
	}), nil
}

func (s *ApiKeyServer) GetApiKey(ctx context.Context, req *connect.Request[pb.GetApiKeyRequest]) (*connect.Response[pb.ApiKey], error) {
	id, err := apiKeyIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	k, err := s.H.GetApiKey(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(apiKeyToProto(k)), nil
}

func (s *ApiKeyServer) ListApiKeys(ctx context.Context, req *connect.Request[pb.ListApiKeysRequest]) (*connect.Response[pb.ListApiKeysResponse], error) {
	m := req.Msg
	tenantID, _ := tenantFromParent(m.GetParent())
	keys, next, err := s.H.ListApiKeys(ctx, authstore.ListApiKeysArgs{
		TenantID:       tenantID,
		PageSize:       m.GetPage().GetPageSize(),
		PageToken:      m.GetPage().GetPageToken(),
		IncludeRevoked: m.GetIncludeRevoked(),
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListApiKeysResponse{Page: pageResponseProto(next)}
	for i := range keys {
		out.ApiKeys = append(out.ApiKeys, apiKeyToProto(&keys[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *ApiKeyServer) RevokeApiKey(ctx context.Context, req *connect.Request[pb.RevokeApiKeyRequest]) (*connect.Response[pb.RevokeApiKeyResponse], error) {
	id, err := apiKeyIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err := s.H.RevokeApiKey(ctx, id); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RevokeApiKeyResponse{}), nil
}

func (s *ApiKeyServer) RotateApiKey(ctx context.Context, req *connect.Request[pb.RotateApiKeyRequest]) (*connect.Response[pb.RotateApiKeyResponse], error) {
	id, err := apiKeyIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	updated, secret, err := s.H.RotateApiKey(ctx, id, req.Msg.GetGracePeriod().AsDuration())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.RotateApiKeyResponse{
		ApiKey:    apiKeyToProto(updated),
		NewSecret: secret,
	}), nil
}

func (s *ApiKeyServer) MintScopedToken(ctx context.Context, req *connect.Request[pb.MintScopedTokenRequest]) (*connect.Response[pb.MintScopedTokenResponse], error) {
	m := req.Msg
	id, err := apiKeyIDFromName(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tok, exp, err := s.H.MintScopedToken(ctx, apikeyh.MintScopedTokenInput{
		ApiKeyID: id,
		Scopes:   scopesFromProto(m.GetScopes()),
		TTL:      m.GetTtl().AsDuration(),
		Audience: m.GetAudience(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.MintScopedTokenResponse{
		AccessToken:      tok,
		ExpiresInSeconds: int32(exp.Unix() - exp.Add(-m.GetTtl().AsDuration()).Unix()),
	}), nil
}

var _ paladiniamv1connect.ApiKeyServiceHandler = (*ApiKeyServer)(nil)

func apiKeyIDFromName(name string) (uuid.UUID, error) {
	// "tenants/{tenant_id}/apiKeys/{api_key_id}"
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "tenants" || parts[2] != "apiKeys" {
		return uuid.Nil, fmt.Errorf("invalid api_key name %q", name)
	}
	return uuid.Parse(parts[3])
}
