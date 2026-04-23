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
// LookupMultipartSession joins multipart_uploads → objects → buckets to
// recover (storage_upload_id, bucket, key); that isn't expressible as a
// single sqlc query, so the adapter falls back to the raw pool.
type PresignRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewPresignRepo(q *sqlc.Queries, pool *pgxpool.Pool) *PresignRepo {
	return &PresignRepo{q: q, pool: pool}
}

var _ presign.Repository = (*PresignRepo)(nil)

func (r *PresignRepo) LookupObjectByName(ctx context.Context, tenantID uuid.UUID, bucketID string, objectID uuid.UUID) (bucket, key, state string, err error) {
	row, err := r.q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		return "", "", "", err
	}
	if row.Object.BucketID != bucketID {
		return "", "", "", fmt.Errorf("object %s not in bucket %s", objectID, bucketID)
	}
	return row.Object.BucketID, row.Object.Key, string(row.Object.State), nil
}

func (r *PresignRepo) LookupMultipartSession(ctx context.Context, uploadID string) (storageUploadID, bucket, key string, err error) {
	const q = `
		SELECT mu.storage_upload_id, o.bucket_id, o.key
		FROM multipart_uploads mu
		JOIN objects o ON o.object_id = mu.object_id
		WHERE mu.upload_id = $1
	`
	err = r.pool.QueryRow(ctx, q, uploadID).Scan(&storageUploadID, &bucket, &key)
	if err != nil {
		if isNoRows(err) {
			return "", "", "", fmt.Errorf("upload %q not found", uploadID)
		}
		return "", "", "", fmt.Errorf("lookup multipart: %w", err)
	}
	return storageUploadID, bucket, key, nil
}
