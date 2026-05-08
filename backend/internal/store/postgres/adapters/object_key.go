package adapters

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// ObjectKeyRepo satisfies objectkey.Repository. Stats require an aggregation that
// isn't expressed in the sqlc query set; we issue it directly through the
// pool.
type ObjectKeyRepo struct {
	q    *sqlc.Queries
	pool *pgxpool.Pool
}

func NewObjectKeyRepo(q *sqlc.Queries, pool *pgxpool.Pool) *ObjectKeyRepo {
	return &ObjectKeyRepo{q: q, pool: pool}
}

var _ objectkey.Repository = (*ObjectKeyRepo)(nil)

func (r *ObjectKeyRepo) Create(ctx context.Context, args objectkey.CreateObjectKeyArgs) (objectkey.ObjectKey, error) {
	// object_keys.lifecycle_rules is JSONB NOT NULL with default '[]'. The SQL
	// INSERT binds this column explicitly, so a nil []byte would surface as
	// NULL and violate the constraint. Normalize to an empty JSON array.
	rules := args.LifecycleRules
	if len(rules) == 0 {
		rules = []byte("[]")
	}
	if err := r.q.CreateObjectKey(ctx,
		pgUUID(args.TenantID),
		args.ObjectKey,
		strPtr(args.DisplayName),
		args.BackendID,
		args.BucketName,
		args.CedarPolicy,
		rules,
	); err != nil {
		return objectkey.ObjectKey{}, fmt.Errorf("create objectKey: %w", err)
	}
	return r.Get(ctx, args.TenantID, args.ObjectKey)
}

func (r *ObjectKeyRepo) Get(ctx context.Context, tenantID uuid.UUID, objectKey string) (objectkey.ObjectKey, error) {
	row, err := r.q.GetObjectKey(ctx, pgUUID(tenantID), objectKey)
	if err != nil {
		return objectkey.ObjectKey{}, err
	}
	return bucketFromSQLC(row.ObjectKey), nil
}

func (r *ObjectKeyRepo) Update(ctx context.Context, args objectkey.UpdateObjectKeyArgs) (objectkey.ObjectKey, error) {
	var policyHash []byte
	if args.CedarPolicy != nil {
		sum := sha256.Sum256([]byte(*args.CedarPolicy))
		policyHash = sum[:]
	}
	rows, err := r.q.UpdateObjectKey(ctx,
		pgUUID(args.TenantID),
		args.ObjectKey,
		args.DisplayName,
		args.CedarPolicy,
		policyHash,
		args.LifecycleRules,
		args.ExpectedVersion,
	)
	if err != nil {
		return objectkey.ObjectKey{}, fmt.Errorf("update objectKey: %w", err)
	}
	if rows == 0 {
		return objectkey.ObjectKey{}, objectkey.ErrVersionMismatch
	}
	return r.Get(ctx, args.TenantID, args.ObjectKey)
}

func (r *ObjectKeyRepo) Delete(ctx context.Context, tenantID uuid.UUID, objectKey string, expectedVersion int64) error {
	rows, err := r.q.DeleteObjectKey(ctx, pgUUID(tenantID), objectKey, expectedVersion)
	if err != nil {
		return fmt.Errorf("delete objectKey: %w", err)
	}
	if rows == 0 {
		return objectkey.ErrVersionMismatch
	}
	return nil
}

func (r *ObjectKeyRepo) Rebind(ctx context.Context, tenantID uuid.UUID, objectKey, backendID, bucketName string, expectedVersion int64) error {
	rows, err := r.q.BindObjectKeyToBucket(ctx, pgUUID(tenantID), objectKey, backendID, bucketName, expectedVersion)
	if err != nil {
		return fmt.Errorf("rebind objectKey: %w", err)
	}
	if rows == 0 {
		return objectkey.ErrVersionMismatch
	}
	return nil
}

func (r *ObjectKeyRepo) List(ctx context.Context, args objectkey.ListObjectKeysArgs) ([]objectkey.ObjectKey, string, error) {
	pageSize := args.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	var after *string
	if args.PageToken != "" {
		tok := args.PageToken
		after = &tok
	}
	rows, err := r.q.ListObjectKeys(ctx, pgUUID(args.TenantID), after, pageSize)
	if err != nil {
		return nil, "", fmt.Errorf("list object_keys: %w", err)
	}
	out := make([]objectkey.ObjectKey, 0, len(rows))
	for _, row := range rows {
		out = append(out, bucketFromSQLC(row.ObjectKey))
	}
	var next string
	if int32(len(out)) == pageSize && len(out) > 0 {
		next = out[len(out)-1].ObjectKey
	}
	return out, next, nil
}

// Stats runs a single grouped aggregation. Returning (AVAILABLE, PENDING,
// DELETED) counts keeps the row count O(1) regardless of objectKey size.
func (r *ObjectKeyRepo) Stats(ctx context.Context, tenantID uuid.UUID, objectKey string) (objectkey.ObjectKeyStats, error) {
	const q = `
		SELECT
			COALESCE(SUM(CASE WHEN state = 'AVAILABLE' THEN 1 ELSE 0 END), 0) AS available,
			COALESCE(SUM(CASE WHEN state = 'PENDING'   THEN 1 ELSE 0 END), 0) AS pending,
			COALESCE(SUM(CASE WHEN state = 'DELETED'   THEN 1 ELSE 0 END), 0) AS deleted,
			COALESCE(SUM(CASE WHEN state = 'AVAILABLE' THEN size_bytes ELSE 0 END), 0) AS size_bytes
		FROM objects
		WHERE tenant_id = $1 AND object_key = $2
	`
	var s objectkey.ObjectKeyStats
	err := r.pool.QueryRow(ctx, q, pgUUID(tenantID), objectKey).Scan(
		&s.ObjectCountAvailable,
		&s.ObjectCountPending,
		&s.ObjectCountDeleted,
		&s.SizeBytesAvailable,
	)
	if err != nil {
		return objectkey.ObjectKeyStats{}, fmt.Errorf("objectKey stats: %w", err)
	}
	return s, nil
}

func bucketFromSQLC(b sqlc.ObjectKey) objectkey.ObjectKey {
	return objectkey.ObjectKey{
		TenantID:        uuidFrom(b.TenantID),
		ObjectKey:       b.ObjectKey,
		DisplayName:     derefStr(b.DisplayName),
		BackendID:       b.BackendID,
		BucketName:      b.BucketName,
		CedarPolicy:     b.CedarPolicy,
		LifecycleRules:  b.LifecycleRules,
		ResourceVersion: b.ResourceVersion,
		CreatedAt:       timeFrom(b.CreatedAt),
		UpdatedAt:       timeFrom(b.UpdatedAt),
	}
}
