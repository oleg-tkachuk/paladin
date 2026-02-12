package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/oleg-tkachuk/paladin/internal/domain"
)

type MultipartRepo struct {
	db *DB
}

func NewMultipartRepo(db *DB) *MultipartRepo { return &MultipartRepo{db: db} }

func (r *MultipartRepo) Create(ctx context.Context, rec domain.Multipart) error {
	if _, err := r.db.Pool.Exec(ctx, `
        INSERT INTO multipart_uploads (id, tenant_id, object_id, upload_id, bucket, object_key, content_type, part_size_bytes, status, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
    `, rec.ID, rec.TenantID, rec.ObjectID, rec.UploadID, rec.Bucket, rec.ObjectKey, rec.ContentType, rec.PartSize, rec.Status, rec.ExpiresAt); err != nil {
		return fmt.Errorf("create multipart: %w", err)
	}

	return nil
}

func (r *MultipartRepo) GetByUploadID(ctx context.Context, tenantID string, uploadID string) (*domain.Multipart, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_id, upload_id, bucket, object_key, content_type, part_size_bytes, status, created_at, updated_at, expires_at
        FROM multipart_uploads
        WHERE tenant_id=$1 AND upload_id=$2
    `, tenantID, uploadID)

	var rec domain.Multipart
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

func (r *MultipartRepo) ListParts(ctx context.Context, multipartID uuid.UUID) ([]domain.MultipartPart, error) {
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

	var out []domain.MultipartPart

	for rows.Next() {
		var p domain.MultipartPart
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

func (r *MultipartRepo) CompleteUpload(ctx context.Context, tenantID string, uploadID string, objectID uuid.UUID) error {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Set local tenant_id for RLS if configured
	if _, err := tx.Exec(ctx, fmt.Sprintf("SELECT set_config('app.tenant_id', '%s', true)", tenantID)); err != nil {
		return fmt.Errorf("set tenant_id: %w", err)
	}

	// 1. Mark multipart completed
	tag, err := tx.Exec(ctx, `
        UPDATE multipart_uploads SET status='completed', updated_at=now()
        WHERE tenant_id=$1 AND upload_id=$2 AND status='initiated'
    `, tenantID, uploadID)
	if err != nil {
		return fmt.Errorf("update multipart: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("multipart upload not found or not initiated")
	}

	// 2. Mark object active
	tag, err = tx.Exec(ctx, `
        UPDATE objects SET status='active', updated_at=now()
        WHERE id=$1 AND tenant_id=$2
    `, objectID, tenantID)
	if err != nil {
		return fmt.Errorf("update object: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("object not found")
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

func (r *MultipartRepo) ListExpired(ctx context.Context, limit int) ([]domain.Multipart, error) {
	rows, err := r.db.Pool.Query(ctx, `
        SELECT id, tenant_id, object_id, upload_id, bucket, object_key, content_type, part_size_bytes, status, created_at, updated_at, expires_at
        FROM multipart_uploads
        WHERE status='initiated' AND expires_at < NOW()
        LIMIT $1
    `, limit)
	if err != nil {
		return nil, fmt.Errorf("query expired multipart: %w", err)
	}
	defer rows.Close()

	var out []domain.Multipart
	for rows.Next() {
		var rec domain.Multipart
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.ObjectID, &rec.UploadID, &rec.Bucket, &rec.ObjectKey, &rec.ContentType, &rec.PartSize, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan multipart: %w", err)
		}
		out = append(out, rec)
	}

	return out, rows.Err()
}
