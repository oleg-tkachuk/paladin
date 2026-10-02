package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

type UserServer struct {
	paladiniamv1connect.UnimplementedUserServiceHandler
	H       userHandler
	tenants tenantSlugSource
}

// tenantSlugSource resolves a tenant slug to its row. Satisfied by the
// tenant repository.
type tenantSlugSource interface {
	GetBySlug(ctx context.Context, slug string) (tenanth.Tenant, error)
}

func NewUserServer(h *userh.Handler, tenants tenantSlugSource) *UserServer {
	return &UserServer{H: h, tenants: tenants}
}

// errForeignTenantSlug refuses a slug that is not the caller's own tenant to a
// caller who is not a platform admin. It is returned without a lookup, so the
// answer is the same whether such a tenant exists or not.
var errForeignTenantSlug = errors.New("tenant is not the caller's")

// tenantParent resolves a `tenants/{tenant_id_or_slug}` parent to a tenant id;
// an empty parent is the caller's whole scope. A UUID costs no lookup. A slug
// is resolved without letting the answer reveal which slugs exist: the
// caller's own slug comes from its token, a platform admin's is looked up, and
// anyone else's is refused before any lookup. The handler still authorises
// the resolved tenant.
func (s *UserServer) tenantParent(ctx context.Context, parent string) (uuid.UUID, error) {
	if parent == "" {
		return uuid.Nil, nil
	}
	if !strings.HasPrefix(parent, apiutil.TenantNamePrefix) {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid parent %q (want %s{tenant_id_or_slug})", parent, apiutil.TenantNamePrefix))
	}
	ref, err := apiutil.ParseTenantNameRef(parent)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if ref.HasID() {
		return ref.ID, nil
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if p != nil && p.TenantSlug != "" && p.TenantSlug == ref.Slug {
		return p.TenantID, nil
	}
	if p == nil || !p.HasRole(apiutil.RolePlatformAdmin) {
		return uuid.Nil, connect.NewError(connect.CodePermissionDenied, errForeignTenantSlug)
	}
	t, err := s.tenants.GetBySlug(ctx, ref.Slug)
	if errors.Is(err, tenanth.ErrNotFound) {
		return uuid.Nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("tenant %q not found", ref.Slug))
	}
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantID, nil
}

func (s *UserServer) CreateUser(ctx context.Context, req *connect.Request[pb.CreateUserRequest]) (*connect.Response[pb.User], error) {
	m := req.Msg
	tenantID, err := s.tenantParent(ctx, m.GetParent())
	if err != nil {
		return nil, err
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

// updateUserPaths are the UpdateUserRequest fields UpdateUser applies.
var updateUserPaths = []string{"display_name", "disabled", "roles"}

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
	if err := convx.CheckMask(m.GetUpdateMask().GetPaths(), updateUserPaths); err != nil {
		return nil, err
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
	// Empty parent is cross-tenant, and the handler enforces the role for it. A
	// parent that does not parse is refused: it used to be dropped, which turned
	// a request for one tenant's users into a listing of every tenant's.
	tenantID, err := s.tenantParent(ctx, m.GetParent())
	if err != nil {
		return nil, err
	}
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

func userIDFromName(name string) (uuid.UUID, error) {
	// Format: tenants/{tenant_id}/users/{user_id}
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "tenants" || parts[2] != "users" {
		return uuid.Nil, fmt.Errorf("invalid user name %q", name)
	}
	return uuid.Parse(parts[3])
}
