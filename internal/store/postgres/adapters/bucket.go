package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// BucketRepo satisfies bucket.Repository. Stats require an aggregation that
// isn't expressed in the sqlc query set; we issue it directly through the
// pool.
type BucketRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewBucketRepo(q *sqlc.Queries, pool *pgxpool.Pool) *BucketRepo {
	return &BucketRepo{q: q, pool: pool}
}

var _ bucket.Repository = (*BucketRepo)(nil)

func (r *BucketRepo) Create(ctx context.Context, args bucket.CreateBucketArgs) (bucket.Bucket, error) {
	if err := r.q.CreateBucket(ctx,
		pgUUID(args.TenantID),
		args.BucketID,
		strPtr(args.DisplayName),
		args.StorageBackend,
		args.CedarPolicy,
		args.LifecycleRules,
	); err != nil {
		return bucket.Bucket{}, fmt.Errorf("create bucket: %w", err)
	}
	return r.Get(ctx, args.TenantID, args.BucketID)
}

func (r *BucketRepo) Get(ctx context.Context, tenantID uuid.UUID, bucketID string) (bucket.Bucket, error) {
	row, err := r.q.GetBucket(ctx, pgUUID(tenantID), bucketID)
	if err != nil {
		return bucket.Bucket{}, err
	}
	return bucketFromSQLC(row.Bucket), nil
}

func (r *BucketRepo) Update(ctx context.Context, args bucket.UpdateBucketArgs) (bucket.Bucket, error) {
	var policyHash []byte
	if args.CedarPolicy != nil {
		sum := sha256.Sum256([]byte(*args.CedarPolicy))
		policyHash = sum[:]
	}
	rows, err := r.q.UpdateBucket(ctx,
		pgUUID(args.TenantID),
		args.BucketID,
		args.DisplayName,
		args.CedarPolicy,
		policyHash,
		args.LifecycleRules,
		args.ExpectedVersion,
	)
	if err != nil {
		return bucket.Bucket{}, fmt.Errorf("update bucket: %w", err)
	}
	if rows == 0 {
		return bucket.Bucket{}, bucket.ErrVersionMismatch
	}
	return r.Get(ctx, args.TenantID, args.BucketID)
}

func (r *BucketRepo) Delete(ctx context.Context, tenantID uuid.UUID, bucketID string, expectedVersion int64) error {
	rows, err := r.q.DeleteBucket(ctx, pgUUID(tenantID), bucketID, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete bucket: %w", err)
	}
	if rows == 0 {
		return bucket.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepo) List(ctx context.Context, args bucket.ListBucketsArgs) ([]bucket.Bucket, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	var after *string
	if args.PageToken != "" {
		tok := args.PageToken
		after = &tok
	}
	rows, err := r.q.ListBuckets(ctx, pgUUID(args.TenantID), after, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list buckets: %w", err)
	}
	out := make([]bucket.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, bucketFromSQLC(row.Bucket))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].BucketID
	}
	return out, next, nil
}

// Stats runs a single grouped aggregation. Returning (AVAILABLE, PENDING,
// DELETED) counts keeps the row count O(1) regardless of bucket size.
func (r *BucketRepo) Stats(ctx context.Context, tenantID uuid.UUID, bucketID string) (bucket.BucketStats, error) {
	const q = `
		SELECT
			COALESCE(SUM(CASE WHEN state = 'AVAILABLE' THEN 1 ELSE 0 END), 0) AS available,
			COALESCE(SUM(CASE WHEN state = 'PENDING'   THEN 1 ELSE 0 END), 0) AS pending,
			COALESCE(SUM(CASE WHEN state = 'DELETED'   THEN 1 ELSE 0 END), 0) AS deleted,
			COALESCE(SUM(CASE WHEN state = 'AVAILABLE' THEN size_bytes ELSE 0 END), 0) AS size_bytes
		FROM objects
		WHERE tenant_id = $1 AND bucket_id = $2
	`
	var s bucket.BucketStats
	err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), bucketID).Scan(
		&s.ObjectCountAvailable,
		&s.ObjectCountPending,
		&s.ObjectCountDeleted,
		&s.SizeBytesAvailable,
	)
	if err != nil {
		return bucket.BucketStats{}, fmt.Errorf("bucket stats: %w", err)
	}
	return s, nil
}

func bucketFromSQLC(b sqlc.Bucket) bucket.Bucket {
	return bucket.Bucket{
		TenantID:        uuidFrom(b.TenantID),
		BucketID:        b.BucketID,
		DisplayName:     derefStr(b.DisplayName),
		StorageBackend:  b.StorageBackend,
		CedarPolicy:     b.CedarPolicy,
		LifecycleRules:  b.LifecycleRules,
		ResourceVersion: b.ResourceVersion,
		CreatedAt:       timeFrom(b.CreatedAt),
		UpdatedAt:       timeFrom(b.UpdatedAt),
	}
}
