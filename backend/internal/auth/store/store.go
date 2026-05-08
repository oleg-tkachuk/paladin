// Package store defines the persistence seams for IAM.
//
// The interfaces are deliberately small — a User row, an ApiKey row, a
// RefreshToken row. Postgres adapters live in internal/store/postgres/iam.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
)

// ─── User ───────────────────────────────────────────────────────────────────

type User struct {
	UserID          uuid.UUID
	TenantID        uuid.UUID
	Subject         string
	DisplayName     string
	PasswordHash    []byte // bcrypt; empty for federated users
	Roles           []string
	Scopes          []auth.Scope
	Disabled        bool
	ResourceVersion int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastLoginAt     *time.Time
}

type UserRepository interface {
	Create(ctx context.Context, u User) (User, error)
	GetByID(ctx context.Context, id uuid.UUID) (User, error)
	GetBySubject(ctx context.Context, tenantID uuid.UUID, subject string) (User, error)
	// FindBySubjectGlobal looks up users across all tenants by subject. Returns
	// 0/1/many — caller resolves ambiguity. Capped to 5 rows so a malicious
	// scan cannot enumerate the user table via repeated calls.
	FindBySubjectGlobal(ctx context.Context, subject string) ([]User, error)
	Update(ctx context.Context, u User, expectedVersion int64) (User, error)
	Delete(ctx context.Context, id uuid.UUID, expectedVersion int64) error
	List(ctx context.Context, args ListUsersArgs) ([]User, string, error)
	UpdatePasswordHash(ctx context.Context, id uuid.UUID, hash []byte) error
	TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error
}

type ListUsersArgs struct {
	TenantID  uuid.UUID // uuid.Nil = cross-tenant (platform-admin)
	PageSize  int32
	PageToken string
	Filter    string // CEL
}

// ─── ApiKey ─────────────────────────────────────────────────────────────────

type ApiKey struct {
	ApiKeyID      uuid.UUID
	TenantID      uuid.UUID
	DisplayPrefix string
	Description   string
	SecretHash    []byte // hash of the secret value; comparison via constant-time
	Roles         []string
	Scopes        []auth.Scope
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	LastUsedAt    *time.Time
	Revoked       bool
}

type ApiKeyRepository interface {
	Create(ctx context.Context, k ApiKey) (ApiKey, error)
	GetByID(ctx context.Context, id uuid.UUID) (ApiKey, error)
	GetByPrefix(ctx context.Context, prefix string) (ApiKey, error)
	List(ctx context.Context, args ListApiKeysArgs) ([]ApiKey, string, error)
	Revoke(ctx context.Context, id uuid.UUID) error
	UpdateSecretHash(ctx context.Context, id uuid.UUID, newHash []byte, oldGraceUntil time.Time) error
	TouchUse(ctx context.Context, id uuid.UUID, at time.Time) error
}

type ListApiKeysArgs struct {
	TenantID       uuid.UUID
	PageSize       int32
	PageToken      string
	IncludeRevoked bool
}

// ─── Refresh tokens ─────────────────────────────────────────────────────────

// RefreshTokenRepository tracks issued refresh tokens so the IAM service can
// revoke them on rotation, password change, or logout. The hashed `jti` is
// the primary key — never store the token value itself.
type RefreshTokenRepository interface {
	Insert(ctx context.Context, t RefreshToken) error
	Get(ctx context.Context, jti uuid.UUID) (RefreshToken, error)
	Revoke(ctx context.Context, jti uuid.UUID) error
	RevokeForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	PurgeExpired(ctx context.Context, olderThan time.Time) (int64, error)
}

type RefreshToken struct {
	JTI       uuid.UUID
	UserID    uuid.UUID
	TenantID  uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
	Revoked   bool
}

// ─── Common errors ──────────────────────────────────────────────────────────

var (
	ErrNotFound        = errors.New("auth/store: not found")
	ErrVersionMismatch = errors.New("auth/store: resource_version mismatch")
	ErrSubjectTaken    = errors.New("auth/store: subject already exists in tenant")
	ErrTokenRevoked    = errors.New("auth/store: refresh token revoked")
)
