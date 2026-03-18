package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

type MultipartRepo struct {
	db *DB
}

func NewMultipartRepo(db *DB) *MultipartRepo { return &MultipartRepo{db: db} }

func (r *MultipartRepo) Create(ctx context.Context, rec domain.Multipart) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "CreateMultipart", status, start) }()

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

	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *MultipartRepo) GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "GetMultipartByUploadID", status, start) }()

	mp, err := r.db.Queries.GetMultipartByUploadID(ctx, tenantID, uploadID)
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	result, err := mapToDomainMultipart(mp.MultipartUpload)
	if err != nil {
		status = domain.StatusError

		return nil, err
	}

	status = domain.StatusSuccess

	return &result, nil
}

func (r *MultipartRepo) UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "UpsertMultipartPart", status, start) }()

	if partNumber > 2147483647 {
		return fmt.Errorf("part number %d is too large", partNumber)
	}
	err := r.db.Queries.UpsertMultipartPart(ctx, uuidToPgtype(multipartID), safecast.Int32(partNumber), &etag, sizeBytes)
	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *MultipartRepo) ListParts(ctx context.Context, multipartID uuid.UUID) ([]domain.MultipartPart, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "ListMultipartParts", status, start) }()

	rows, err := r.db.Queries.ListMultipartParts(ctx, uuidToPgtype(multipartID))
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	out := make([]domain.MultipartPart, 0, len(rows))
	for _, row := range rows {
		part, err := mapToDomainMultipartPart(row.MultipartPart)
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("map multipart part: %w", err)
		}
		out = append(out, part)
	}

	status = domain.StatusSuccess

	return out, nil
}

func (r *MultipartRepo) MarkCompleted(ctx context.Context, tenantID string, uploadID string) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkMultipartCompleted", status, start) }()

	err := r.db.Queries.MarkMultipartCompleted(ctx, tenantID, uploadID)
	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *MultipartRepo) MarkAborted(ctx context.Context, tenantID string, uploadID string) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "MarkMultipartAborted", status, start) }()

	err := r.db.Queries.MarkMultipartAborted(ctx, tenantID, uploadID)
	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return mapPgError(err)
}

func (r *MultipartRepo) CompleteUpload(ctx context.Context, tenantID string, uploadID string, objectID uuid.UUID) error {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "CompleteUpload_Tx", status, start) }()

	// Use WithTx for transaction support
	err := r.db.WithTx(ctx, func(q *sqlc.Queries) error {
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

	if err != nil {
		status = domain.StatusError
	} else {
		status = domain.StatusSuccess
	}

	return err
}

func (r *MultipartRepo) ListExpired(ctx context.Context, limit int) ([]domain.Multipart, error) {
	start := time.Now()
	var status string
	defer func() { metrics.RecordDbQuery(ctx, "ListExpiredMultiparts", status, start) }()

	rows, err := r.db.Queries.ListExpiredMultiparts(ctx, safecast.Int32(limit))
	if err != nil {
		status = domain.StatusError

		return nil, mapPgError(err)
	}

	out := make([]domain.Multipart, 0, len(rows))
	for _, row := range rows {
		mp, err := mapToDomainMultipart(row.MultipartUpload)
		if err != nil {
			status = domain.StatusError

			return nil, fmt.Errorf("map multipart: %w", err)
		}
		out = append(out, mp)
	}

	status = domain.StatusSuccess

	return out, nil
}
