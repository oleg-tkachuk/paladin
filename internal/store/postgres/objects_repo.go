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
        INSERT INTO objects (id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, expires_at)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
    `, rec.ID, rec.TenantID, rec.ObjectKey, rec.Bucket, rec.ContentType, rec.SizeBytes, rec.ChecksumSHA256, rec.Status, rec.ExpiresAt); err != nil {
		return fmt.Errorf("create object: %w", err)
	}

	return nil
}

func (r *ObjectsRepo) Get(ctx context.Context, tenantID string, id uuid.UUID) (*ObjectRecord, error) {
	row := r.db.Pool.QueryRow(ctx, `
        SELECT id, tenant_id, object_key, bucket, content_type, size_bytes, checksum_sha256, status, created_at, updated_at, expires_at
        FROM objects
        WHERE tenant_id=$1 AND id=$2
    `, tenantID, id)

	var rec ObjectRecord
	if err := row.Scan(&rec.ID, &rec.TenantID, &rec.ObjectKey, &rec.Bucket, &rec.ContentType, &rec.SizeBytes, &rec.ChecksumSHA256, &rec.Status, &rec.CreatedAt, &rec.UpdatedAt, &rec.ExpiresAt); err != nil {
		return nil, fmt.Errorf("scan object: %w", err)
	}

	return &rec, nil
}

func (r *ObjectsRepo) MarkActive(ctx context.Context, tenantID string, id uuid.UUID) error {
	if _, err := r.db.Pool.Exec(ctx, `
        UPDATE objects SET status='active', updated_at=now()
        WHERE tenant_id=$1 AND id=$2
    `, tenantID, id); err != nil {
		return fmt.Errorf("mark object active: %w", err)
	}

	return nil
}
