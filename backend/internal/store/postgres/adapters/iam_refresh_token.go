package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

type RefreshTokenRepo struct {
	q *sqlc.Queries
}

func NewRefreshTokenRepo(q *sqlc.Queries) *RefreshTokenRepo { return &RefreshTokenRepo{q: q} }

var _ authstore.RefreshTokenRepository = (*RefreshTokenRepo)(nil)

func (r *RefreshTokenRepo) Insert(ctx context.Context, t authstore.RefreshToken) error {
	return r.q.InsertRefreshToken(ctx,
		pgUUID(t.JTI),
		pgUUID(t.UserID),
		pgUUID(t.TenantID),
		pgUUID(t.FamilyID),
		pgTS(t.IssuedAt),
		pgTS(t.ExpiresAt),
	)
}

func (r *RefreshTokenRepo) Get(ctx context.Context, jti uuid.UUID) (authstore.RefreshToken, error) {
	row, err := r.q.GetRefreshToken(ctx, pgUUID(jti))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authstore.RefreshToken{}, authstore.ErrNotFound
		}
		return authstore.RefreshToken{}, err
	}
	if row.Revoked {
		return authstore.RefreshToken{}, authstore.ErrTokenRevoked
	}
	return refreshTokenFromSQLC(row), nil
}

func refreshTokenFromSQLC(row sqlc.RefreshToken) authstore.RefreshToken {
	out := authstore.RefreshToken{
		JTI:       uuidFrom(row.ID),
		FamilyID:  uuidFrom(row.FamilyID),
		UserID:    uuidFrom(row.UserID),
		TenantID:  uuidFrom(row.TenantID),
		IssuedAt:  timeFrom(row.IssuedAt),
		ExpiresAt: timeFrom(row.ExpiresAt),
		Revoked:   row.Revoked,
	}
	if row.SupersededAt.Valid {
		at := timeFrom(row.SupersededAt)
		out.SupersededAt = &at
	}
	return out
}

func (r *RefreshTokenRepo) Revoke(ctx context.Context, jti uuid.UUID) error {
	return r.q.RevokeRefreshToken(ctx, pgUUID(jti))
}

// Supersede marks the token as traded in, but only if it is still live. Zero
// rows updated means another caller got there first — the caller lost a
// rotation race rather than presenting a stolen token, and ErrAlreadyRotated
// says so.
func (r *RefreshTokenRepo) Supersede(ctx context.Context, jti uuid.UUID) error {
	n, err := r.q.SupersedeRefreshToken(ctx, pgUUID(jti))
	if err != nil {
		return err
	}
	if n == 0 {
		return authstore.ErrAlreadyRotated
	}
	return nil
}

// GetAny is Get without the revoked check — the caller decides what a revoked
// token means, which is the whole point of superseded_at.
func (r *RefreshTokenRepo) GetAny(ctx context.Context, jti uuid.UUID) (authstore.RefreshToken, error) {
	row, err := r.q.GetRefreshToken(ctx, pgUUID(jti))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authstore.RefreshToken{}, authstore.ErrNotFound
		}
		return authstore.RefreshToken{}, err
	}
	return refreshTokenFromSQLC(row), nil
}

func (r *RefreshTokenRepo) RevokeForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	return r.q.RevokeRefreshTokensForUser(ctx, pgUUID(userID))
}

func (r *RefreshTokenRepo) RevokeFamilyOf(ctx context.Context, jti uuid.UUID) (int64, error) {
	return r.q.RevokeRefreshTokenFamily(ctx, pgUUID(jti))
}

func (r *RefreshTokenRepo) PurgeExpired(ctx context.Context, olderThan time.Time) (int64, error) {
	return r.q.PurgeExpiredRefreshTokens(ctx, pgTS(olderThan))
}
