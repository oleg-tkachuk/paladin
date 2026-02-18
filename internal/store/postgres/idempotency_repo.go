package postgres

import (
	"context"

	"github.com/oleg-tkachuk/paladin/internal/domain"
)

type IdempotencyRepo struct {
	db *DB
}

func NewIdempotencyRepo(db *DB) *IdempotencyRepo { return &IdempotencyRepo{db: db} }

func (r *IdempotencyRepo) Get(ctx context.Context, tenantID string, key string) (*domain.IdempotencyRecord, error) {
	rec, err := r.db.Queries.GetIdempotencyKey(ctx, tenantID, key)
	if err != nil {
		return nil, MapPgError(err)
	}

	result := MapIdempotencyToDomain(rec)
	return &result, nil
}

func (r *IdempotencyRepo) Save(ctx context.Context, rec domain.IdempotencyRecord) error {
	err := r.db.Queries.UpsertIdempotencyKey(ctx,
		rec.TenantID,
		rec.Key,
		rec.RequestPath,
		rec.RequestHash,
		int32(rec.ResponseCode),
		rec.ResponseBody,
		timestampToPgtype(rec.ExpiresAt),
	)

	return MapPgError(err)
}

func (r *IdempotencyRepo) Delete(ctx context.Context, tenantID string, key string) error {
	err := r.db.Queries.DeleteIdempotencyKey(ctx, tenantID, key)
	return MapPgError(err)
}
