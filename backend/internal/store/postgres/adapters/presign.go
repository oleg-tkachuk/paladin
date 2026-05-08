package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// PresignRepo satisfies presign.Repository.
//
// LookupMultipartSession joins multipart_uploads → objects → object_keys to
// recover (storage_upload_id, objectKey, key); that isn't expressible as a
// single sqlc query, so the adapter falls back to the raw pool.
type PresignRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewPresignRepo(q *sqlc.Queries, pool *pgxpool.Pool) *PresignRepo {
	return &PresignRepo{q: q, pool: pool}
}

var _ presign.Repository = (*PresignRepo)(nil)

func (r *PresignRepo) LookupObjectByName(ctx context.Context, tenantID uuid.UUID, objectKey string, objectID uuid.UUID) (resolvedObjectKey, key, state string, err error) {
	row, err := r.q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		return "", "", "", err
	}
	if row.Object.ObjectKey != objectKey {
		return "", "", "", fmt.Errorf("object %s not in objectKey %s", objectID, objectKey)
	}
	return row.Object.ObjectKey, row.Object.Key, string(row.Object.State), nil
}

func (r *PresignRepo) LookupMultipartSession(ctx context.Context, uploadID string) (storageUploadID, objectKey, key string, err error) {
	const q = `
		SELECT mu.storage_upload_id, o.object_key, o.key
		FROM multipart_uploads mu
		JOIN objects o ON o.object_id = mu.object_id
		WHERE mu.upload_id = $1
	`
	err = r.pool.QueryRow(ctx, q, uploadID).Scan(&storageUploadID, &objectKey, &key)
	if err != nil {
		if isNoRows(err) {
			return "", "", "", fmt.Errorf("upload %q not found", uploadID)
		}
		return "", "", "", fmt.Errorf("lookup multipart: %w", err)
	}
	return storageUploadID, objectKey, key, nil
}

// LookupBucket reads the physical S3 bucket bound to an ObjectKey via
// idx_object_keys_bucket_routing. bucket_name is NOT NULL after
// migration 005 so a successful lookup always returns a non-empty value.
func (r *PresignRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, objectKey string) (string, error) {
	const q = `SELECT bucket_name FROM object_keys WHERE tenant_id = $1 AND object_key = $2`
	var bucket string
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), objectKey).Scan(&bucket); err != nil {
		if isNoRows(err) {
			return "", fmt.Errorf("objectKey %q not found", objectKey)
		}
		return "", fmt.Errorf("lookup bucket: %w", err)
	}
	return bucket, nil
}
