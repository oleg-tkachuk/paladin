package adapters

import (
	"context"
	"fmt"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/bucket"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// BucketRepo satisfies bucket.Repository.
type BucketRepo struct {
	q *sqlc.Queries
}

func NewBucketRepo(q *sqlc.Queries) *BucketRepo { return &BucketRepo{q: q} }

var _ bucket.Repository = (*BucketRepo)(nil)

func (r *BucketRepo) Create(ctx context.Context, args bucket.CreateArgs) (bucket.Bucket, error) {
	// labels is JSONB NOT NULL DEFAULT '{}'. Bind explicitly to avoid SQL NULL.
	labels := args.Labels
	if len(labels) == 0 {
		labels = []byte("{}")
	}
	if err := r.q.CreateBucket(ctx,
		args.BackendID,
		args.BucketName,
		strPtr(args.DisplayName),
		strPtr(args.Region),
		labels,
	); err != nil {
		return bucket.Bucket{}, fmt.Errorf("create bucket: %w", err)
	}
	return r.Get(ctx, args.BackendID, args.BucketName)
}

func (r *BucketRepo) Get(ctx context.Context, backendID, bucketName string) (bucket.Bucket, error) {
	row, err := r.q.GetBucket(ctx, backendID, bucketName)
	if err != nil {
		return bucket.Bucket{}, err
	}
	return BucketFromSQLC(row.Bucket), nil
}

func (r *BucketRepo) Update(ctx context.Context, args bucket.UpdateArgs) (bucket.Bucket, error) {
	rows, err := r.q.UpdateBucket(ctx,
		args.BackendID,
		args.BucketName,
		args.DisplayName,
		args.Labels,
		args.ExpectedVersion,
	)
	if err != nil {
		return bucket.Bucket{}, fmt.Errorf("update bucket: %w", err)
	}
	if rows == 0 {
		return bucket.Bucket{}, bucket.ErrVersionMismatch
	}
	return r.Get(ctx, args.BackendID, args.BucketName)
}

func (r *BucketRepo) Delete(ctx context.Context, backendID, bucketName string, expectedVersion int64) error {
	rows, err := r.q.DeleteBucket(ctx, backendID, bucketName, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete bucket: %w", err)
	}
	if rows == 0 {
		return bucket.ErrVersionMismatch
	}
	return nil
}

func (r *BucketRepo) List(ctx context.Context, args bucket.ListArgs) ([]bucket.Bucket, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	var afterBackend, afterName *string
	if args.PageToken != "" {
		// page_token format: "<backend_id>/<bucket_name>"
		// Simple implementation: split on first '/'.
		for i := 0; i < len(args.PageToken); i++ {
			if args.PageToken[i] == '/' {
				ab, an := args.PageToken[:i], args.PageToken[i+1:]
				afterBackend, afterName = &ab, &an
				break
			}
		}
	}
	rows, err := r.q.ListBuckets(ctx, args.BackendID, afterName, afterBackend, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list buckets: %w", err)
	}
	out := make([]bucket.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, BucketFromSQLC(row.Bucket))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		last := out[len(out)-1]
		next = last.BackendID + "/" + last.BucketName
	}
	return out, next, nil
}

func (r *BucketRepo) CountCollections(ctx context.Context, backendID, bucketName string) (int64, error) {
	n, err := r.q.CountCollectionsReferencingBucket(ctx, backendID, bucketName)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func BucketFromSQLC(b sqlc.Bucket) bucket.Bucket {
	return bucket.Bucket{
		BackendID:       b.BackendID,
		BucketName:      b.BucketName,
		DisplayName:     derefStr(b.DisplayName),
		Region:          derefStr(b.Region),
		Labels:          b.Labels,
		ResourceVersion: b.ResourceVersion,
		CreatedAt:       timeFrom(b.CreatedAt),
		UpdatedAt:       timeFrom(b.UpdatedAt),
	}
}
