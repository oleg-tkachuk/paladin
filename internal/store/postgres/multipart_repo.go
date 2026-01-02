package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type MultipartStatus string

const (
	MultipartInitiated MultipartStatus = "initiated"
	MultipartCompleted MultipartStatus = "completed"
	MultipartAborted   MultipartStatus = "aborted"
	MultipartExpired   MultipartStatus = "expired"
)

type MultipartRecord struct {
	ID          uuid.UUID
	TenantID    string
	ObjectID    uuid.UUID
	UploadID    string
	Bucket      string
	ObjectKey   string
	ContentType string
	PartSize    int64
	Status      MultipartStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ExpiresAt   time.Time
}

type MultipartPartRecord struct {
	MultipartID uuid.UUID
	PartNumber  int
	ETag        *string
	SizeBytes   *int64
	CreatedAt   time.Time
}

type MultipartRepo struct {
	db *DB
}

func NewMultipartRepo(db *DB) *MultipartRepo { return &MultipartRepo{db: db} }

func (r *MultipartRepo) Create(ctx context.Context, rec MultipartRecord) error {
	if _, err := r.db.Pool.Exec(ctx, `
        INSERT INTO multipart_uploads (id, tenant_id, object_id, upload_id, bucket, object_key, content_type, part_size_bytes, status, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    `, rec.ID, rec.TenantID, rec.ObjectID, rec.UploadID, rec.Bucket, rec.ObjectKey, rec.ContentType, rec.PartSize, rec.Status, rec.ExpiresAt); err != nil {
		return fmt.Errorf("create multipart: %w", err)
	}

	return nil
}

func (r *MultipartRepo) GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*MultipartRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_id, upload_id, bucket, object_key, content_type, part_size_bytes, status, created_at, updated_at, expires_at
        FROM multipart_uploads
        WHERE tenant_id=$1 AND upload_id=$2
    `, tenantID, uploadID)

	var rec MultipartRecord
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.ObjectID, &rec.UploadID, &rec.Bucket, &rec.ObjectKey, &rec.ContentType, &rec.PartSize, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt); err != nil {
		return nil, fmt.Errorf("scan multipart: %w", err)
	}

	return &rec, nil
}

func (r *MultipartRepo) UpsertPartETag(ctx context.Context, multipartID uuid.UUID, partNumber int, etag string, sizeBytes *int64) error {
	if _, err := r.db.Pool.Exec(ctx, `
        INSERT INTO multipart_parts (multipart_id, part_number, etag, size_bytes)
        VALUES ($1,$2,$3,$4)
        ON CONFLICT (multipart_id, part_number)
        DO UPDATE SET etag=EXCLUDED.etag, size_bytes=EXCLUDED.size_bytes
    `, multipartID, partNumber, etag, sizeBytes); err != nil {
		return fmt.Errorf("upsert part etag: %w", err)
	}

	return nil
}

func (r *MultipartRepo) ListParts(ctx context.Context, multipartID uuid.UUID) ([]MultipartPartRecord, error) {
	rows, err := r.db.Pool.Query(ctx, `
        SELECT multipart_id, part_number, etag, size_bytes, created_at
        FROM multipart_parts
        WHERE multipart_id=$1
        ORDER BY part_number ASC
    `, multipartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MultipartPartRecord

	for rows.Next() {
		var p MultipartPartRecord
		if err := rows.Scan(&p.MultipartID, &p.PartNumber, &p.ETag, &p.SizeBytes, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan multipart part: %w", err)
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return out, nil
}

func (r *MultipartRepo) MarkCompleted(ctx context.Context, tenantID string, uploadID string) error {
	if _, err := r.db.Pool.Exec(ctx, `
        UPDATE multipart_uploads SET status='completed', updated_at=now()
        WHERE tenant_id=$1 AND upload_id=$2
    `, tenantID, uploadID); err != nil {
		return fmt.Errorf("mark multipart completed: %w", err)
	}

	return nil
}

func (r *MultipartRepo) MarkAborted(ctx context.Context, tenantID string, uploadID string) error {
	if _, err := r.db.Pool.Exec(ctx, `
        UPDATE multipart_uploads SET status='aborted', updated_at=now()
        WHERE tenant_id=$1 AND upload_id=$2
    `, tenantID, uploadID); err != nil {
		return fmt.Errorf("mark multipart aborted: %w", err)
	}

	return nil
}
