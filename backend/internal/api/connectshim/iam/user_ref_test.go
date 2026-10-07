package iam

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// refUser records the UserRef each operation receives.
type refUser struct {
	failingUser
	got userh.UserRef
}

func (r *refUser) GetUser(_ context.Context, ref userh.UserRef) (*authstore.User, error) {
	r.got = ref
	return &authstore.User{}, nil
}

func (r *refUser) UpdateUser(_ context.Context, in userh.UpdateUserInput) (*authstore.User, error) {
	r.got = in.User
	return &authstore.User{}, nil
}

func (r *refUser) DeleteUser(_ context.Context, ref userh.UserRef, _ int64) error {
	r.got = ref
	return nil
}

func (r *refUser) GrantScopes(_ context.Context, ref userh.UserRef, _ []auth.Scope) (*authstore.User, error) {
	r.got = ref
	return &authstore.User{}, nil
}

func (r *refUser) RevokeScopes(_ context.Context, ref userh.UserRef, _ []auth.Scope) (*authstore.User, error) {
	r.got = ref
	return &authstore.User{}, nil
}

func (r *refUser) ResetPassword(_ context.Context, ref userh.UserRef, _ string) (string, error) {
	r.got = ref
	return "", nil
}

// Every user operation hands the handler the tenant its name carries — by id,
// or by slug resolved to the id — so the handler can refuse a user who is not
// that tenant's. A shim that kept only the user id is how a trashed tenant's
// user was reachable past the freeze under a live tenant's name.
func TestUserOperationsCarryTheNamedTenant(t *testing.T) {
	const slug = "globex"
	tenant, user := uuid.New(), uuid.New()
	admin := asCaller(uuid.New(), "platform", apiutil.RolePlatformAdmin)

	for spelling, ref := range map[string]string{"id": tenant.String(), "slug": slug} {
		name := "tenants/" + ref + "/users/" + user.String()
		ops := map[string]func(*UserServer) error{
			"GetUser": func(s *UserServer) error {
				_, err := s.GetUser(admin, &pb.GetUserRequest{Name: name})
				return err
			},
			"UpdateUser": func(s *UserServer) error {
				_, err := s.UpdateUser(admin, &pb.UpdateUserRequest{Name: name})
				return err
			},
			"DeleteUser": func(s *UserServer) error {
				_, err := s.DeleteUser(admin, &pb.DeleteUserRequest{Name: name})
				return err
			},
			"GrantScopes": func(s *UserServer) error {
				_, err := s.GrantScopes(admin, &pb.GrantScopesRequest{Name: name})
				return err
			},
			"RevokeScopes": func(s *UserServer) error {
				_, err := s.RevokeScopes(admin, &pb.RevokeScopesRequest{Name: name})
				return err
			},
			"ResetPassword": func(s *UserServer) error {
				_, err := s.ResetPassword(admin, &pb.ResetPasswordRequest{Name: name})
				return err
			},
		}
		for op, call := range ops {
			t.Run(spelling+"/"+op, func(t *testing.T) {
				h := &refUser{}
				srv := &UserServer{H: h, tenants: &countingSlugs{slug: slug, id: tenant}}
				if err := call(srv); err != nil {
					t.Fatal(err)
				}
				if want := (userh.UserRef{TenantID: tenant, UserID: user}); h.got != want {
					t.Errorf("handler got %+v, want %+v", h.got, want)
				}
			})
		}
	}
}
