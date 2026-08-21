package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/presign"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// PresignRepo satisfies presign.Repository.
//
// LookupMultipartSession joins multipart_uploads → objects → collections to
// recover (storage_upload_id, collection, key); that isn't expressible as a
// single sqlc query, so the adapter falls back to the raw pool.
type PresignRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewPresignRepo(q *sqlc.Queries, pool *pgxpool.Pool) *PresignRepo {
	return &PresignRepo{q: q, pool: pool}
}

var _ presign.Repository = (*PresignRepo)(nil)

func (r *PresignRepo) LookupObjectByName(ctx context.Context, tenantID uuid.UUID, collection string, objectID uuid.UUID) (resolvedCollection, key, state string, err error) {
	row, err := r.q.GetObject(ctx, pgUUID(tenantID), pgUUID(objectID))
	if err != nil {
		return "", "", "", err
	}
	if row.CollectionName != collection {
		return "", "", "", fmt.Errorf("object %s not in collection %s", objectID, collection)
	}
	return row.CollectionName, row.Object.Path, string(row.Object.State), nil
}

func (r *PresignRepo) LookupMultipartSession(ctx context.Context, uploadID string) (storageUploadID, collection, key string, err error) {
	const q = `
		SELECT mu.storage_upload_id, c.name, o.path
		FROM multipart_uploads mu
		JOIN objects o     ON o.id = mu.object_id
		JOIN collections c ON c.id = o.collection_id
		WHERE mu.id = $1
	`
	err = r.pool.QueryRow(ctx, q, uploadID).Scan(&storageUploadID, &collection, &key)
	if err != nil {
		if isNoRows(err) {
			return "", "", "", fmt.Errorf("upload %q not found", uploadID)
		}
		return "", "", "", fmt.Errorf("lookup multipart: %w", err)
	}
	return storageUploadID, collection, key, nil
}

// LookupBucket reads the physical S3 bucket bound to a Collection via
// idx_collections_bucket_routing. bucket_name is NOT NULL after
// the schema baseline (001_initial_schema.sql) so a successful lookup always returns a non-empty value.
// `write` splits the read-only-drain gate (the schema baseline (001_initial_schema.sql)): presign-GET is a
// read, presign-PUT / presign-part are writes. Both the disabled (feature
// 002) and drain gates are enforced here so a presign URL is never issued
// against a backend that can't serve the op.
func (r *PresignRepo) LookupBucket(ctx context.Context, tenantID uuid.UUID, collection string, write bool) (string, string, error) {
	const q = `
		SELECT sb.name, bk.name, sb.enabled, sb.read_only, bk.provision_state
		FROM collections c
		JOIN buckets bk          ON bk.id = c.bucket_id
		JOIN storage_backends sb ON sb.id = bk.backend_id
		WHERE c.tenant_id = $1 AND c.name = $2`
	var (
		backendID      string
		bucket         string
		enabled        bool
		readOnly       bool
		provisionState string
	)
	if err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), collection).Scan(&backendID, &bucket, &enabled, &readOnly, &provisionState); err != nil {
		if isNoRows(err) {
			return "", "", fmt.Errorf("collection %q not found", collection)
		}
		return "", "", fmt.Errorf("lookup bucket: %w", err)
	}
	if !enabled {
		return "", "", object.ErrBackendDisabled
	}
	if write && readOnly {
		return "", "", object.ErrBackendReadOnly
	}
	if write && provisionState != "ready" {
		return "", "", object.ErrBucketProvisioning
	}
	return backendID, bucket, nil
}
