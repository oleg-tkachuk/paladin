package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type MultipartRepo struct {
	db *DB
}

func NewMultipartRepo(db *DB) *MultipartRepo { return &MultipartRepo{db: db} }

func (r *MultipartRepo) Create(ctx context.Context, rec domain.Multipart) error {
	err := r.db.Queries.CreateMultipart(ctx,
		uuidToPgtype(rec.ID),
		rec.TenantID,
		uuidToPgtype(rec.ObjectID),
		rec.UploadID,
		rec.Bucket,
		rec.ObjectKey,
		rec.ContentType,
		rec.PartSize,
		string(rec.Status),
		timestampToPgtype(rec.ExpiresAt),
	)

	return MapPgError(err)
}

func (r *MultipartRepo) GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	mp, err := r.db.Queries.GetMultipartByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		return nil, MapPgError(err)
	}

	result, err := MapMultipartToDomain(mp)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (r *MultipartRepo) UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error {
	err := r.db.Queries.UpsertMultipartPart(ctx, uuidToPgtype(multipartID), int32(partNumber), &etag, sizeBytes)
	return MapPgError(err)
}

func (r *MultipartRepo) ListParts(ctx context.Context, multipartID uuid.UUID) ([]domain.MultipartPart, error) {
	rows, err := r.db.Queries.ListMultipartParts(ctx, uuidToPgtype(multipartID))
	if err != nil {
		return nil, MapPgError(err)
	}

	out := make([]domain.MultipartPart, 0, len(rows))
	for _, row := range rows {
		part, err := MapMultipartPartToDomain(row)
		if err != nil {
			return nil, fmt.Errorf("map multipart part: %w", err)
		}
		out = append(out, part)
	}

	return out, nil
}

func (r *MultipartRepo) MarkCompleted(ctx context.Context, tenantID string, uploadID string) error {
	err := r.db.Queries.MarkMultipartCompleted(ctx, tenantID, uploadID)
	return MapPgError(err)
}

func (r *MultipartRepo) MarkAborted(ctx context.Context, tenantID string, uploadID string) error {
	err := r.db.Queries.MarkMultipartAborted(ctx, tenantID, uploadID)
	return MapPgError(err)
}

func (r *MultipartRepo) CompleteUpload(ctx context.Context, tenantID string, uploadID string, objectID uuid.UUID) error {
	// Use WithTx for transaction support
	return r.db.WithTx(ctx, func(q *sqlc.Queries) error {
		// Set local tenant_id for RLS if configured
		if _, err := r.db.Pool.Exec(ctx, fmt.Sprintf("SELECT set_config('app.tenant_id', '%s', true)", tenantID)); err != nil {
			return fmt.Errorf("set tenant_id: %w", err)
		}

		// 1. Mark multipart completed
		rows, err := q.UpdateMultipartStatus(ctx, tenantID, uploadID)
		if err != nil {
			return fmt.Errorf("update multipart: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("multipart upload not found or not initiated")
		}

		// 2. Mark object active
		rows, err = q.UpdateObjectStatusToActive(ctx, uuidToPgtype(objectID), tenantID)
		if err != nil {
			return fmt.Errorf("update object: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("object not found")
		}

		return nil
	})
}

func (r *MultipartRepo) ListExpired(ctx context.Context, limit int) ([]domain.Multipart, error) {
	rows, err := r.db.Queries.ListExpiredMultiparts(ctx, int32(limit))
	if err != nil {
		return nil, MapPgError(err)
	}

	out := make([]domain.Multipart, 0, len(rows))
	for _, row := range rows {
		mp, err := MapMultipartToDomain(row)
		if err != nil {
			return nil, fmt.Errorf("map multipart: %w", err)
		}
		out = append(out, mp)
	}

	return out, nil
}
