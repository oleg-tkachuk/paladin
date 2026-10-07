package iam

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/authh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// The property, for every handler-backed server on the IAM plane: a handler
// failure must reach the caller AS a failure, wrapping the original.
//
// The stake here is higher than on the other two planes. A data-plane shim
// that swallowed an error answers with an empty list; these answer with a
// token pair, a changed password, or a switched tenant. `Login` returning a
// zero-valued LoginResponse is a client holding an empty bearer token it
// believes is valid, and a server that logged nothing.
//
// Before the seam none of these branches were reachable: authh.Handler needs
// a database and a signing key, so only the slow gate touched them.

var errBoom = errors.New("identity store unavailable")

type failingAuth struct{}

func (failingAuth) ChangePassword(context.Context, string, string) error {
	return errBoom
}
func (failingAuth) ExchangeAudience(context.Context, authh.ExchangeAudienceInput) (*authh.ExchangeAudienceOutput, error) {
	return nil, errBoom
}
func (failingAuth) ListMyMemberships(context.Context, authh.ListMembershipsInput) ([]authh.Membership, string, error) {
	return nil, "", errBoom
}
func (failingAuth) Login(context.Context, authh.LoginInput) (*authh.LoginOutput, error) {
	return nil, errBoom
}
func (failingAuth) RefreshToken(context.Context, authh.RefreshInput) (*authh.RefreshOutput, error) {
	return nil, errBoom
}
func (failingAuth) Revoke(context.Context, string) error {
	return errBoom
}
func (failingAuth) SwitchTenant(context.Context, uuid.UUID, string) (*authh.SwitchTenantOutput, error) {
	return nil, errBoom
}
func (failingAuth) WhoAmI(context.Context, string) (*authh.WhoAmIOutput, error) {
	return nil, errBoom
}

type failingUser struct{}

func (failingUser) CreateUser(context.Context, userh.CreateUserInput) (*authstore.User, error) {
	return nil, errBoom
}
func (failingUser) DeleteUser(context.Context, uuid.UUID, int64) error {
	return errBoom
}
func (failingUser) GetUser(context.Context, uuid.UUID) (*authstore.User, error) {
	return nil, errBoom
}
func (failingUser) GrantScopes(context.Context, uuid.UUID, []auth.Scope) (*authstore.User, error) {
	return nil, errBoom
}
func (failingUser) ListUsers(context.Context, userh.ListUsersInput) ([]authstore.User, string, error) {
	return nil, "", errBoom
}
func (failingUser) ResetPassword(context.Context, uuid.UUID, string) (string, error) {
	return "", errBoom
}
func (failingUser) RevokeScopes(context.Context, uuid.UUID, []auth.Scope) (*authstore.User, error) {
	return nil, errBoom
}
func (failingUser) UpdateUser(context.Context, userh.UpdateUserInput) (*authstore.User, error) {
	return nil, errBoom
}

type failingUserSettings struct{}

func (failingUserSettings) DeleteForUser(context.Context, uuid.UUID) error {
	return errBoom
}
func (failingUserSettings) GetForUser(context.Context, uuid.UUID) (*usersettingsh.Settings, error) {
	return nil, errBoom
}
func (failingUserSettings) GetMine(context.Context) (*usersettingsh.Settings, error) {
	return nil, errBoom
}
func (failingUserSettings) ListByTenant(context.Context, uuid.UUID, int32) ([]usersettingsh.Settings, error) {
	return nil, errBoom
}
func (failingUserSettings) UpdateMine(context.Context, usersettingsh.UpdateMineInput) (*usersettingsh.Settings, error) {
	return nil, errBoom
}

// The doubles must satisfy the same interfaces the servers hold, or the table
// below would be exercising something the production wiring never uses.
var (
	_ authHandler         = failingAuth{}
	_ userHandler         = failingUser{}
	_ userSettingsHandler = failingUserSettings{}
)

func TestEveryIAMShimPropagatesHandlerErrors(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	userID := uuid.New()
	userName := "tenants/" + tenantID.String() + "/users/" + userID.String()
	settingsName := "users/" + userID.String()

	authSrv := &AuthServer{H: failingAuth{}}
	userSrv := &UserServer{H: failingUser{}}
	setSrv := &UserSettingsServer{H: failingUserSettings{}}

	cases := []struct {
		name string
		call func() error
	}{
		{"Auth.Login", func() error {
			_, err := authSrv.Login(ctx, &pb.LoginRequest{
				Subject: "tester", Password: "hunter2",
			})
			return err
		}},
		{"Auth.RefreshToken", func() error {
			_, err := authSrv.RefreshToken(ctx, &pb.RefreshTokenRequest{
				RefreshToken: "rt",
			})
			return err
		}},
		{"Auth.Revoke", func() error {
			_, err := authSrv.Revoke(ctx, &pb.RevokeRequest{Token: "rt"})
			return err
		}},
		{"Auth.WhoAmI", func() error {
			_, err := authSrv.WhoAmI(ctx, &pb.WhoAmIRequest{})
			return err
		}},
		{"Auth.ChangePassword", func() error {
			_, err := authSrv.ChangePassword(ctx, &pb.ChangePasswordRequest{
				OldPassword: "old", NewPassword: "new",
			})
			return err
		}},
		{"Auth.ExchangeAudience", func() error {
			_, err := authSrv.ExchangeAudience(ctx, &pb.ExchangeAudienceRequest{
				RefreshToken: "rt", TargetAudience: "paladin-data",
			})
			return err
		}},
		{"Auth.ListMyMemberships", func() error {
			_, err := authSrv.ListMyMemberships(ctx, &pb.ListMyMembershipsRequest{})
			return err
		}},
		{"Auth.SwitchTenant", func() error {
			_, err := authSrv.SwitchTenant(ctx, &pb.SwitchTenantRequest{
				TargetTenantId: tenantID.String(),
			})
			return err
		}},

		{"User.CreateUser", func() error {
			_, err := userSrv.CreateUser(ctx, &pb.CreateUserRequest{
				Parent: "tenants/" + tenantID.String(), Subject: "tester",
			})
			return err
		}},
		{"User.GetUser", func() error {
			_, err := userSrv.GetUser(ctx, &pb.GetUserRequest{Name: userName})
			return err
		}},
		{"User.UpdateUser", func() error {
			_, err := userSrv.UpdateUser(ctx, &pb.UpdateUserRequest{
				Name: userName, ResourceVersion: "1",
			})
			return err
		}},
		{"User.DeleteUser", func() error {
			_, err := userSrv.DeleteUser(ctx, &pb.DeleteUserRequest{
				Name: userName, ResourceVersion: "1",
			})
			return err
		}},
		{"User.ListUsers", func() error {
			_, err := userSrv.ListUsers(ctx, &pb.ListUsersRequest{
				Parent: "tenants/" + tenantID.String(),
			})
			return err
		}},
		{"User.GrantScopes", func() error {
			_, err := userSrv.GrantScopes(ctx, &pb.GrantScopesRequest{Name: userName})
			return err
		}},
		{"User.RevokeScopes", func() error {
			_, err := userSrv.RevokeScopes(ctx, &pb.RevokeScopesRequest{Name: userName})
			return err
		}},
		{"User.ResetPassword", func() error {
			_, err := userSrv.ResetPassword(ctx, &pb.ResetPasswordRequest{Name: userName})
			return err
		}},

		{"UserSettings.GetMine", func() error {
			_, err := setSrv.GetMine(ctx, &pb.GetMineRequest{})
			return err
		}},
		{"UserSettings.UpdateMine", func() error {
			_, err := setSrv.UpdateMine(ctx, &pb.UpdateMineRequest{Theme: "dark"})
			return err
		}},
		{"UserSettings.GetForUser", func() error {
			_, err := setSrv.GetForUser(ctx, &pb.GetForUserRequest{Name: settingsName})
			return err
		}},
		{"UserSettings.ListByTenant", func() error {
			_, err := setSrv.ListByTenant(ctx, &pb.ListByTenantRequest{
				Parent: "tenants/" + tenantID.String(),
			})
			return err
		}},
		{"UserSettings.DeleteForUser", func() error {
			_, err := setSrv.DeleteForUser(ctx, &pb.DeleteForUserRequest{
				Name: settingsName,
			})
			return err
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.call()
			if err == nil {
				t.Fatal("the handler failed and the shim answered success — on this " +
					"plane that hands the caller a zero-valued token pair, or reports " +
					"a password change that never happened")
			}
			if !errors.Is(err, errBoom) {
				t.Errorf("error %v does not wrap the handler's — either the shim "+
					"replaced the cause, or this request never reached the handler "+
					"and the case is testing argument parsing instead", err)
			}
		})
	}
}
