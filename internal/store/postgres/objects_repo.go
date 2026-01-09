package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ObjectStatus string

const (
	ObjectPending ObjectStatus = "pending"
	ObjectActive  ObjectStatus = "active"
	ObjectDeleted ObjectStatus = "deleted"
)

type ObjectRecord struct {
	ID             uuid.UUID
	TenantID       string
	ObjectKey      string
	Bucket         string
	ContentType    string
	SizeBytes      int64
	ChecksumSHA256 *string
	Status         ObjectStatus
	Labels         map[string]string
	ExternalRef    *string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExpiresAt      *time.Time
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
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref
        FROM objects
        WHERE status='pending' AND created_at < $1
        LIMIT $2
    `, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("query expired pending: %w", err)
	}
	defer rows.Close()

	var out []ObjectRecord
	for rows.Next() {
		var rec ObjectRecord
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef); err != nil {
			return nil, fmt.Errorf("scan object: %w", err)
		}
		out = append(out, rec)
	}

	return out, rows.Err()
}

func (r *ObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*ObjectRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref
        FROM objects
        WHERE tenant_id=$1 AND id=$2
    `, tenantID, id)

	var rec ObjectRecord
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef); err != nil {
		return nil, fmt.Errorf("scan object: %w", err)
	}

	return &rec, nil
}

func (r *ObjectsRepo) MarkActive(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
        UPDATE objects SET status='active', updated_at=now()
        WHERE tenant_id=$1 AND id=$2 AND status='pending'
    `, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("mark object active: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}

func (r *ObjectsRepo) MarkDeleted(ctx context.Context, tenantID string, id uuid.UUID) (bool, error) {
	tag, err := r.db.Pool.Exec(ctx, `
        UPDATE objects SET status='deleted', updated_at=now()
        WHERE tenant_id=$1 AND id=$2 AND status IN ('pending', 'active')
    `, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("mark object deleted: %w", err)
	}

	return tag.RowsAffected() > 0, nil
}

func (r *ObjectsRepo) GetByExternalRef(ctx context.Context, tenantID string, externalRef string) (*ObjectRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at, labels, external_ref
        FROM objects
        WHERE tenant_id=$1 AND external_ref=$2
    `, tenantID, externalRef)

	var rec ObjectRecord
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt, &rec.Labels, &rec.ExternalRef); err != nil {
		return nil, fmt.Errorf("scan object: %w", err)
	}

	return &rec, nil
}
