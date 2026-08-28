// Package store defines the persistence seams for IAM.
//
// The interfaces are deliberately small — a User row, a RefreshToken row.
// Postgres adapters live in internal/store/postgres/iam.
package store

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
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
	// ListMembershipsBySubject returns one page of the tenants a subject
	// belongs to, ordered by membership creation, with the cursor being the
	// previous page's last (created_at, id). FindBySubjectGlobal caps its
	// result — it exists to detect an ambiguous login, not to enumerate — so
	// this is a separate query.
	//
	// Callers that need to answer "is this subject a member of tenant X"
	// should use GetBySubject instead of scanning this: SwitchTenant used to
	// walk the whole list, which is why it broke when an earlier version
	// capped the query at five rows.
	ListMembershipsBySubject(ctx context.Context, subject string, afterCreated time.Time, afterID uuid.UUID, limit int32) ([]User, error)
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
	// Filter is the caller's CEL expression. The store pushes its
	// SQL-expressible conjuncts into the query so the predicate selects from
	// the table rather than from one page; userh still evaluates the whole
	// expression over the returned rows, which stays authoritative.
	//
	// It sat here unread for a long time before that — the field existed at
	// every layer and was applied at none, so the RPC accepted a filter that
	// did nothing.
	Filter string
}

// ─── Refresh tokens ─────────────────────────────────────────────────────────

// RefreshTokenRepository tracks issued refresh tokens so the IAM service can
// revoke them on rotation, password change, or logout. The hashed `jti` is
// the primary key — never store the token value itself.
type RefreshTokenRepository interface {
	Insert(ctx context.Context, t RefreshToken) error
	Get(ctx context.Context, jti uuid.UUID) (RefreshToken, error)
	Revoke(ctx context.Context, jti uuid.UUID) error

	// Supersede marks a token as rotated for a successor. Distinct from Revoke
	// on purpose: revocation for cause must never be tolerated, while a token
	// its own holder just traded in may be, briefly, by the non-consuming
	// paths — see ExchangeAudience.
	Supersede(ctx context.Context, jti uuid.UUID) error

	// GetAny returns the row whether or not it is revoked. Get refuses a
	// revoked token, which is right for the consuming paths and useless for a
	// caller that needs to ask WHY it was revoked.
	GetAny(ctx context.Context, jti uuid.UUID) (RefreshToken, error)
	RevokeForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	// RevokeFamilyOf revokes every still-live token sharing the family of the
	// given jti — the reuse-detection chain revocation (ADR-0009). Returns the
	// number of tokens revoked.
	RevokeFamilyOf(ctx context.Context, jti uuid.UUID) (int64, error)
	PurgeExpired(ctx context.Context, olderThan time.Time) (int64, error)
}

type RefreshToken struct {
	JTI uuid.UUID
	// FamilyID groups a login + all its subsequent rotations. A new login
	// starts a family; each rotation inherits it. Reuse-detection revokes by
	// family so only the compromised chain dies (ADR-0009).
	FamilyID  uuid.UUID
	UserID    uuid.UUID
	TenantID  uuid.UUID
	IssuedAt  time.Time
	ExpiresAt time.Time
	Revoked   bool
	// SupersededAt is set only when this token was ROTATED for a successor —
	// never by logout and never by reuse detection. It is what lets a handler
	// tell "the holder traded this in three seconds ago" from "this token was
	// killed for cause", which the Revoked boolean alone cannot express.
	SupersededAt *time.Time
}

// ─── Common errors ──────────────────────────────────────────────────────────

var (
	ErrNotFound        = errors.New("auth/store: not found")
	ErrVersionMismatch = errors.New("auth/store: resource_version mismatch")
	ErrSubjectTaken    = errors.New("auth/store: subject already exists in tenant")
	ErrTokenRevoked    = errors.New("auth/store: refresh token revoked")
)

// Register the IAM store sentinels with the central error→Connect-code mapper
// (ADR-0002). ErrNotFound→NotFound is the generic mapping; authh deliberately
// maps it to Unauthenticated INLINE on the login/refresh paths (so it never
// reaches MapError), which is why both coexist. ErrTokenRevoked is handled
// inline (reuse-detection) and intentionally not registered.
func init() {
	apiutil.RegisterError(ErrNotFound, connect.CodeNotFound)
	apiutil.RegisterError(ErrVersionMismatch, connect.CodeAborted)
	apiutil.RegisterError(ErrSubjectTaken, connect.CodeAlreadyExists)
}
