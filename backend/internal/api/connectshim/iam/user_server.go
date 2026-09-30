package iam

import (
	"context"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

type UserServer struct {
	paladiniamv1connect.UnimplementedUserServiceHandler
	H userHandler
}

func NewUserServer(h *userh.Handler) *UserServer { return &UserServer{H: h} }

func (s *UserServer) CreateUser(ctx context.Context, req *connect.Request[pb.CreateUserRequest]) (*connect.Response[pb.User], error) {
	m := req.Msg
	tenantID, err := tenantFromParent(m.GetParent())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	out, err := s.H.CreateUser(ctx, userh.CreateUserInput{
		TenantID:        tenantID,
		Subject:         m.GetSubject(),
		DisplayName:     m.GetDisplayName(),
		InitialPassword: m.GetInitialPassword(),
		Roles:           m.GetRoles(),
		Scopes:          scopesFromProto(m.GetScopes()),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(userToProto(out)), nil
}

func (s *UserServer) GetUser(ctx context.Context, req *connect.Request[pb.GetUserRequest]) (*connect.Response[pb.User], error) {
	id, err := userIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	u, err := s.H.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(userToProto(u)), nil
}

func (s *UserServer) UpdateUser(ctx context.Context, req *connect.Request[pb.UpdateUserRequest]) (*connect.Response[pb.User], error) {
	m := req.Msg
	id, err := userIDFromName(m.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	out, err := s.H.UpdateUser(ctx, userh.UpdateUserInput{
		UserID:          id,
		ExpectedVersion: rv,
		UpdateMask:      m.GetUpdateMask().GetPaths(),
		DisplayName:     m.GetDisplayName(),
		Disabled:        m.GetDisabled(),
		Roles:           m.GetRoles(),
	})
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(userToProto(out)), nil
}

func (s *UserServer) DeleteUser(ctx context.Context, req *connect.Request[pb.DeleteUserRequest]) (*connect.Response[pb.DeleteUserResponse], error) {
	id, err := userIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rv, err := convx.ParseRV(req.Msg.GetResourceVersion())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := s.H.DeleteUser(ctx, id, rv); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.DeleteUserResponse{}), nil
}

func (s *UserServer) ListUsers(ctx context.Context, req *connect.Request[pb.ListUsersRequest]) (*connect.Response[pb.ListUsersResponse], error) {
	m := req.Msg
	tenantID, _ := tenantFromParent(m.GetParent()) // empty parent → cross-tenant; handler enforces role
	users, next, err := s.H.ListUsers(ctx, userh.ListUsersInput{
		TenantID:  tenantID,
		PageSize:  m.GetPage().GetPageSize(),
		PageToken: m.GetPage().GetPageToken(),
		Filter:    m.GetFilter(),
	})
	if err != nil {
		return nil, err
	}
	out := &pb.ListUsersResponse{
		Page: convx.PageResponseProto(next),
	}
	for i := range users {
		out.Users = append(out.Users, userToProto(&users[i]))
	}
	return connect.NewResponse(out), nil
}

func (s *UserServer) GrantScopes(ctx context.Context, req *connect.Request[pb.GrantScopesRequest]) (*connect.Response[pb.User], error) {
	id, err := userIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	u, err := s.H.GrantScopes(ctx, id, scopesFromProto(req.Msg.GetScopes()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(userToProto(u)), nil
}

func (s *UserServer) RevokeScopes(ctx context.Context, req *connect.Request[pb.RevokeScopesRequest]) (*connect.Response[pb.User], error) {
	id, err := userIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	u, err := s.H.RevokeScopes(ctx, id, scopesFromProto(req.Msg.GetScopes()))
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(userToProto(u)), nil
}

func (s *UserServer) ResetPassword(ctx context.Context, req *connect.Request[pb.ResetPasswordRequest]) (*connect.Response[pb.ResetPasswordResponse], error) {
	id, err := userIDFromName(req.Msg.GetName())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	pw, err := s.H.ResetPassword(ctx, id, req.Msg.GetNewPassword())
	if err != nil {
		return nil, err
	}
	out := &pb.ResetPasswordResponse{}
	if req.Msg.GetNewPassword() == "" {
		out.GeneratedPassword = pw
	}
	return connect.NewResponse(out), nil
}

var _ paladiniamv1connect.UserServiceHandler = (*UserServer)(nil)

// silence unused imports if only some shims compile
var _ = authstore.User{}

// ─── helpers ────────────────────────────────────────────────────────────────

func tenantFromParent(parent string) (uuid.UUID, error) {
	if parent == "" {
		return uuid.Nil, nil
	}
	const prefix = "tenants/"
	if !strings.HasPrefix(parent, prefix) {
		return uuid.Nil, fmt.Errorf("invalid parent %q (want tenants/{id})", parent)
	}
	return uuid.Parse(parent[len(prefix):])
}

func userIDFromName(name string) (uuid.UUID, error) {
	// Format: tenants/{tenant_id}/users/{user_id}
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "tenants" || parts[2] != "users" {
		return uuid.Nil, fmt.Errorf("invalid user name %q", name)
	}
	return uuid.Parse(parts[3])
}
