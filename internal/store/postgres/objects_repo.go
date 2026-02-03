package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ListObjectsFilter struct {
	Status        *ObjectStatus
	ExternalRef   *string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

type ObjectStatus string

const (
	ObjectPending   ObjectStatus = "pending"
	ObjectUploading ObjectStatus = "uploading"
	ObjectUploaded  ObjectStatus = "uploaded"
	ObjectComplete  ObjectStatus = "complete"
	ObjectAborted   ObjectStatus = "aborted"
	ObjectDeleted   ObjectStatus = "deleted"
	ObjectError     ObjectStatus = "error"
)

type ObjectRecord struct {
	ID              uuid.UUID
	TenantID        string
	ObjectKey       string
	Bucket          string
	ContentType     string
	SizeBytes       int64
	ChecksumSHA256  *string
	Status          ObjectStatus
	Labels          map[string]string
	ExternalRef     *string
	StoredETag      *string
	StoredSizeBytes *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
	ExpiresAt       *time.Time
	CompletedAt     *time.Time
	DeletedAt       *time.Time
}

type ObjectsRepo struct {
	db *DB
}

func NewObjectsRepo(db *DB) *ObjectsRepo { return &ObjectsRepo{db: db} }

func (r *ObjectsRepo) Create(ctx context.Context, rec ObjectRecord) error {
	if _, err := r.db.Pool.Exec(ctx, `
        INSERT INTO objects (id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, expires_at, labels, external_ref)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
    `, rec.ID, rec.TenantID, rec.ObjectKey, rec.Bucket, rec.ContentType, rec.SizeBytes, rec.ChecksumSHA256, rec.Status, rec.ExpiresAt, rec.Labels, rec.ExternalRef); err != nil {
		return fmt.Errorf("create object: %w", err)
	}

	return nil
}

func (r *ObjectsRepo) ListExpiredPending(ctx context.Context, cutoff time.Time, limit int) ([]ObjectRecord, error) {
	rows, err := r.db.Pool.Query(ctx, `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at
        FROM objects
        WHERE status='pending' AND expires_at < $1
        LIMIT $2
    `, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query expired pending: %w", err)
	}
	defer rows.Close()

	var out []ObjectRecord
	for rows.Next() {
		var rec ObjectRecord
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef, &rec.StoredETag, &rec.StoredSizeBytes, &rec.CompletedAt, &rec.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan object: %w", err)
		}
		out = append(out, rec)
	}

	return out, rows.Err()
}

func (r *ObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*ObjectRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at
        FROM objects
        WHERE tenant_id=$1 AND id=$2
    `, tenantID, id)

	var rec ObjectRecord
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef, &rec.StoredETag, &rec.StoredSizeBytes, &rec.CompletedAt, &rec.DeletedAt); err != nil {
		return nil, fmt.Errorf("scan object: %w", err)
	}

	return &rec, nil
}

func (r *ObjectsRepo) MarkComplete(ctx context.Context, tenantID string, id uuid.UUID, etag string, sizeBytes int64) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
        UPDATE objects 
        SET status='complete', stored_etag=$3, stored_size_bytes=$4, completed_at=now(), updated_at=now()
        WHERE tenant_id=$1 AND id=$2 AND (status='pending' OR status='uploading')
    `, tenantID, id, etag, sizeBytes)
	if err != nil {
		return false, fmt.Errorf("mark object complete: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}

func (r *ObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
        UPDATE objects SET status='deleted', deleted_at=now(), updated_at=now()
        WHERE tenant_id=$1 AND id=$2 AND status != 'deleted'
    `, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("mark object deleted: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}

func (r *ObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*ObjectRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at
        FROM objects
        WHERE tenant_id=$1 AND external_ref=$2
    `, tenantID, externalRef)

	var rec ObjectRecord
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef, &rec.StoredETag, &rec.StoredSizeBytes, &rec.CompletedAt, &rec.DeletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan object: %w", err)
	}

	return &rec, nil
}

func (r *ObjectsRepo) List(ctx context.Context, tenantID string, filter ListObjectsFilter, limit int, cursor string) ([]ObjectRecord, string, error) {
	query := `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at
        FROM objects
        WHERE tenant_id=$1
    `
	args := []interface{}{tenantID}
	argIdx := 2

	if filter.Status != nil {
		query += fmt.Sprintf(" AND status=$%d", argIdx)
		args = append(args, *filter.Status)
		argIdx++
	}
	if filter.ExternalRef != nil {
		query += fmt.Sprintf(" AND external_ref=$%d", argIdx)
		args = append(args, *filter.ExternalRef)
		argIdx++
	}
	if filter.CreatedAfter != nil {
		query += fmt.Sprintf(" AND created_at >= $%d", argIdx)
		args = append(args, *filter.CreatedAfter)
		argIdx++
	}
	if filter.CreatedBefore != nil {
		query += fmt.Sprintf(" AND created_at < $%d", argIdx)
		args = append(args, *filter.CreatedBefore)
		argIdx++
	}

	if cursor != "" {
		// Simple cursor: CreatedAt. For production, use (CreatedAt, ID) for stable sort.
		query += fmt.Sprintf(" AND created_at < $%d", argIdx)
		// We'd need to parse the cursor here. For now, let's assume it's an RFC3339 string.
		t, err := time.Parse(time.RFC3339, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, t)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argIdx)
	args = append(args, limit+1)

	rows, err := r.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query objects: %w", err)
	}
	defer rows.Close()

	var out []ObjectRecord
	for rows.Next() {
		var rec ObjectRecord
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef, &rec.StoredETag, &rec.StoredSizeBytes, &rec.CompletedAt, &rec.DeletedAt); err != nil {
			return nil, "", fmt.Errorf("scan object: %w", err)
		}
		out = append(out, rec)
	}

	nextCursor := ""
	if len(out) > limit {
		nextCursor = out[limit-1].CreatedAt.Format(time.RFC3339)
		out = out[:limit]
	}

	return out, nextCursor, rows.Err()
}

func (r *ObjectsRepo) Patch(ctx context.Context, tenantID string, id uuid.UUID, labels map[string]string, externalRef *string) (*ObjectRecord, error) {
	// Simple implementation: fetch, update, save. For atomicity, use a single UPDATE with COALESCE or similar.
	// But labels is a map, so we might want to merge. The spec says "Update object metadata (labels/external_ref)".
	// Usually PATCH means partial update.

	query := `UPDATE objects SET updated_at=now()`
	args := []interface{}{tenantID, id}
	argIdx := 3

	if labels != nil {
		// Postgres JSONB merge: labels = labels || $3
		query += fmt.Sprintf(", labels = labels || $%d", argIdx)
		args = append(args, labels)
		argIdx++
	}
	if externalRef != nil {
		query += fmt.Sprintf(", external_ref = $%d", argIdx)
		args = append(args, *externalRef)
		argIdx++
	}

	query += " WHERE tenant_id=$1 AND id=$2 RETURNING id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref, stored_etag, stored_size_bytes, completed_at, deleted_at"

	var rec ObjectRecord
	err := r.db.Pool.QueryRow(ctx, query, args...).Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef, &rec.StoredETag, &rec.StoredSizeBytes, &rec.CompletedAt, &rec.DeletedAt)
	if err != nil {
		return nil, fmt.Errorf("patch object: %w", err)
	}

	return &rec, nil
}
