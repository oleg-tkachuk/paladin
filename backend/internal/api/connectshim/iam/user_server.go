package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/connectshim/convx"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"

	"connectrpc.com/connect/v2"
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
		return uuid.Nil, connect.Errorf(connect.CodeInvalidArgument,
			"invalid parent %q (want %s{tenant_id_or_slug})", parent, apiutil.TenantNamePrefix)
	}
	ref, err := apiutil.ParseTenantNameRef(parent)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if ref.HasID() {
		return ref.ID, nil
	}
	p, _ := auth.PrincipalFromContext(ctx)
	if p != nil && p.TenantSlug != "" && p.TenantSlug == ref.Slug {
		return p.TenantID, nil
	}
	if p == nil || !p.HasRole(apiutil.RolePlatformAdmin) {
		return uuid.Nil, connect.NewError(connect.CodePermissionDenied, errForeignTenantSlug.Error()).WithCause(errForeignTenantSlug)
	}
	t, err := s.tenants.GetBySlug(ctx, ref.Slug)
	if errors.Is(err, tenanth.ErrNotFound) {
		return uuid.Nil, connect.Errorf(connect.CodeNotFound, "tenant %q not found", ref.Slug)
	}
	if err != nil {
		return uuid.Nil, err
	}
	return t.TenantID, nil
}

func (s *UserServer) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.User, error) {
	m := req
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
	return userToProto(out), nil
}

func (s *UserServer) GetUser(ctx context.Context, req *pb.GetUserRequest) (*pb.User, error) {
	ref, err := s.userRef(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	u, err := s.H.GetUser(ctx, ref)
	if err != nil {
		return nil, err
	}
	return userToProto(u), nil
}

// updateUserPaths are the UpdateUserRequest fields UpdateUser applies.
var updateUserPaths = []string{"display_name", "disabled", "roles"}

func (s *UserServer) UpdateUser(ctx context.Context, req *pb.UpdateUserRequest) (*pb.User, error) {
	m := req
	ref, err := s.userRef(ctx, m.GetName())
	if err != nil {
		return nil, err
	}
	rv, err := convx.ParseRV(m.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := convx.CheckMask(m.GetUpdateMask().GetPaths(), updateUserPaths); err != nil {
		return nil, err
	}
	out, err := s.H.UpdateUser(ctx, userh.UpdateUserInput{
		User:            ref,
		ExpectedVersion: rv,
		UpdateMask:      m.GetUpdateMask().GetPaths(),
		DisplayName:     m.GetDisplayName(),
		Disabled:        m.GetDisabled(),
		Roles:           m.GetRoles(),
	})
	if err != nil {
		return nil, err
	}
	return userToProto(out), nil
}

func (s *UserServer) DeleteUser(ctx context.Context, req *pb.DeleteUserRequest) (*pb.DeleteUserResponse, error) {
	ref, err := s.userRef(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	rv, err := convx.ParseRV(req.GetResourceVersion())
	if err != nil {
		return nil, rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid resource_version: %w", err))
	}
	if err := s.H.DeleteUser(ctx, ref, rv); err != nil {
		return nil, err
	}
	return &pb.DeleteUserResponse{}, nil
}

func (s *UserServer) ListUsers(ctx context.Context, req *pb.ListUsersRequest) (*pb.ListUsersResponse, error) {
	m := req
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
	return out, nil
}

func (s *UserServer) GrantScopes(ctx context.Context, req *pb.GrantScopesRequest) (*pb.User, error) {
	ref, err := s.userRef(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	u, err := s.H.GrantScopes(ctx, ref, scopesFromProto(req.GetScopes()))
	if err != nil {
		return nil, err
	}
	return userToProto(u), nil
}

func (s *UserServer) RevokeScopes(ctx context.Context, req *pb.RevokeScopesRequest) (*pb.User, error) {
	ref, err := s.userRef(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	u, err := s.H.RevokeScopes(ctx, ref, scopesFromProto(req.GetScopes()))
	if err != nil {
		return nil, err
	}
	return userToProto(u), nil
}

func (s *UserServer) ResetPassword(ctx context.Context, req *pb.ResetPasswordRequest) (*pb.ResetPasswordResponse, error) {
	ref, err := s.userRef(ctx, req.GetName())
	if err != nil {
		return nil, err
	}
	pw, err := s.H.ResetPassword(ctx, ref, req.GetNewPassword())
	if err != nil {
		return nil, err
	}
	out := &pb.ResetPasswordResponse{}
	if req.GetNewPassword() == "" {
		out.GeneratedPassword = pw
	}
	return out, nil
}

var _ paladiniamv1connect.UserServiceHandler = (*UserServer)(nil)

// silence unused imports if only some shims compile
var _ = authstore.User{}

// ─── helpers ────────────────────────────────────────────────────────────────

// userNameParts splits tenants/{tenant_id_or_slug}/users/{user_id} into its
// tenant parent and the user's id.
func userNameParts(name string) (parent string, id uuid.UUID, err error) {
	parts := strings.Split(name, "/")
	if len(parts) != 4 || parts[0] != "tenants" || parts[2] != "users" {
		return "", uuid.Nil, fmt.Errorf("invalid user name %q", name)
	}
	id, err = uuid.Parse(parts[3])
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("invalid user name %q: %w", name, err)
	}
	return apiutil.TenantNamePrefix + parts[1], id, nil
}

// userRef resolves a user's resource name, its tenant by id or slug, so the
// handler can check the user is that tenant's.
func (s *UserServer) userRef(ctx context.Context, name string) (userh.UserRef, error) {
	parent, id, err := userNameParts(name)
	if err != nil {
		return userh.UserRef{}, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	tenant, err := s.tenantParent(ctx, parent)
	if err != nil {
		return userh.UserRef{}, err
	}
	return userh.UserRef{TenantID: tenant, UserID: id}, nil
}
