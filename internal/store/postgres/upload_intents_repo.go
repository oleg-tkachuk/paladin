package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
)

// UploadIntentsRepo persists server-generated upload intents in PostgreSQL.
// Intents are transient rows that represent "we handed out a presigned URL
// for this key"; they are consumed (DELETEd) inside the same transaction
// that INSERTs the final `objects` row on CompleteObject.
type UploadIntentsRepo struct {
	db *DB
}

func NewUploadIntentsRepo(db *DB) *UploadIntentsRepo { return &UploadIntentsRepo{db: db} }

func (r *UploadIntentsRepo) Create(ctx context.Context, rec domain.UploadIntent) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "CreateUploadIntent", status, start) }()

	labels, err := marshalStringMap(rec.Labels)
	if err != nil {
		status = domain.StatusError

		return fmt.Errorf("marshal labels: %w", err)
	}

	tags, err := marshalStringMap(rec.Tags)
	if err != nil {
		status = domain.StatusError

		return fmt.Errorf("marshal tags: %w", err)
	}

	err = r.db.Queries.CreateUploadIntent(ctx,
		uuidToPgtype(rec.ID),
		rec.TenantID,
		rec.Bucket,
		rec.ObjectKey,
		rec.Category,
		rec.Subpath,
		rec.ContentType,
		rec.SizeBytes,
		labels,
		tags,
		rec.ExternalRef,
		rec.IdempotencyKey,
		timestampToPgtype(rec.ExpiresAt),
	)

	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *UploadIntentsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*domain.UploadIntent, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetUploadIntent", status, start) }()

	row, err := r.db.Queries.GetUploadIntent(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			status = domain.StatusNotFound

			return nil, domain.ErrNotFound
		}
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	result, err := mapToDomainUploadIntent(row.ObjectUploadIntent)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *UploadIntentsRepo) GetByKey(ctx context.Context, tenantID, bucket, objectKey string) (*domain.UploadIntent, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetUploadIntentByKey", status, start) }()

	row, err := r.db.Queries.GetUploadIntentByKey(ctx, tenantID, bucket, objectKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			status = domain.StatusNotFound

			return nil, domain.ErrNotFound
		}
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	result, err := mapToDomainUploadIntent(row.ObjectUploadIntent)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *UploadIntentsRepo) GetByIdempotencyKey(ctx context.Context, tenantID, idempotencyKey string) (*domain.UploadIntent, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetUploadIntentByIdempotencyKey", status, start) }()

	key := idempotencyKey
	row, err := r.db.Queries.GetUploadIntentByIdempotencyKey(ctx, tenantID, &key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			status = domain.StatusNotFound

			return nil, domain.ErrNotFound
		}
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	result, err := mapToDomainUploadIntent(row.ObjectUploadIntent)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *UploadIntentsRepo) Delete(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "DeleteUploadIntent", status, start) }()

	rows, err := r.db.Queries.DeleteUploadIntent(ctx, tenantID, uuidToPgtype(id))
	if err != nil {
		status = domain.StatusError

		return false, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows > 0, nil
}

func (r *UploadIntentsRepo) DeleteExpired(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "DeleteExpiredUploadIntents", status, start) }()

	rows, err := r.db.Queries.DeleteExpiredUploadIntents(ctx, timestampToPgtype(cutoff), safecast.Int32(limit))
	if err != nil {
		status = domain.StatusError

		return 0, mapPgError(err)
	}

	status = domain.StatusSuccess

	return rows, nil
}
