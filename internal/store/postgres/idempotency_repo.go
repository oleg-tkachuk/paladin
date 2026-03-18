package postgres

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
)

type IdempotencyRepo struct {
	db *DB
}

func NewIdempotencyRepo(db *DB) *IdempotencyRepo { return &IdempotencyRepo{db: db} }

func (r *IdempotencyRepo) Get(ctx context.Context, tenantID string, key string) (*domain.IdempotencyRecord, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetIdempotencyKey", status, start) }()

	rec, err := r.db.Queries.GetIdempotencyKey(ctx, tenantID, key)
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	status = domain.StatusSuccess
	result := mapToDomainIdempotency(rec.IdempotencyKey)

	return &result, nil
}

func (r *IdempotencyRepo) Save(ctx context.Context, rec domain.IdempotencyRecord) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "UpsertIdempotencyKey", status, start) }()

	err := r.db.Queries.UpsertIdempotencyKey(ctx,
		rec.TenantID,
		rec.Key,
		rec.RequestPath,
		rec.RequestHash,
		safecast.Int32(rec.ResponseCode),
		rec.ResponseBody,
		timestampToPgtype(rec.ExpiresAt),
	)

	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *IdempotencyRepo) Delete(ctx context.Context, tenantID string, key string) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "DeleteIdempotencyKey", status, start) }()

	err := r.db.Queries.DeleteIdempotencyKey(ctx, tenantID, key)
	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}
