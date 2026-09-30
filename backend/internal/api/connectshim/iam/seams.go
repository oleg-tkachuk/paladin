package iam

import (
	"context"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/authh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/userh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/iam/v1/usersettingsh"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
)

// authHandler is what AuthServer needs from *authh.Handler.
type authHandler interface {
	ChangePassword(ctx context.Context, oldPw, newPw string) error
	ExchangeAudience(ctx context.Context, in authh.ExchangeAudienceInput) (*authh.ExchangeAudienceOutput, error)
	ListMyMemberships(ctx context.Context, in authh.ListMembershipsInput) ([]authh.Membership, string, error)
	Login(ctx context.Context, in authh.LoginInput) (*authh.LoginOutput, error)
	RefreshToken(ctx context.Context, in authh.RefreshInput) (*authh.RefreshOutput, error)
	Revoke(ctx context.Context, token string) error
	SwitchTenant(ctx context.Context, targetTenantID uuid.UUID, requestedAudience string) (*authh.SwitchTenantOutput, error)
	WhoAmI(ctx context.Context, routePageToken string) (*authh.WhoAmIOutput, error)
}

var _ authHandler = (*authh.Handler)(nil)

// userHandler is what UserServer needs from *userh.Handler.
type userHandler interface {
	CreateUser(ctx context.Context, in userh.CreateUserInput) (*authstore.User, error)
	DeleteUser(ctx context.Context, id uuid.UUID, expectedVersion int64) error
	GetUser(ctx context.Context, id uuid.UUID) (*authstore.User, error)
	GrantScopes(ctx context.Context, id uuid.UUID, scopes []auth.Scope) (*authstore.User, error)
	ListUsers(ctx context.Context, in userh.ListUsersInput) ([]authstore.User, string, error)
	ResetPassword(ctx context.Context, id uuid.UUID, newPassword string) (string, error)
	RevokeScopes(ctx context.Context, id uuid.UUID, scopes []auth.Scope) (*authstore.User, error)
	UpdateUser(ctx context.Context, in userh.UpdateUserInput) (*authstore.User, error)
}

var _ userHandler = (*userh.Handler)(nil)

// userSettingsHandler is what UserSettingsServer needs from *usersettingsh.Handler.
type userSettingsHandler interface {
	DeleteForUser(ctx context.Context, userID uuid.UUID) error
	GetForUser(ctx context.Context, userID uuid.UUID) (*usersettingsh.Settings, error)
	GetMine(ctx context.Context) (*usersettingsh.Settings, error)
	ListByTenant(ctx context.Context, tenantID uuid.UUID, pageSize int32) ([]usersettingsh.Settings, error)
	UpdateMine(ctx context.Context, in usersettingsh.UpdateMineInput) (*usersettingsh.Settings, error)
}

var _ userSettingsHandler = (*usersettingsh.Handler)(nil)
