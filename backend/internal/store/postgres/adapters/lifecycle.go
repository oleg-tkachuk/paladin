package adapters

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// LifecycleSource implements worker.BucketLifecycleSource.
type LifecycleSource struct {
	q *sqlc.Queries
}

func NewLifecycleSource(q *sqlc.Queries) *LifecycleSource { return &LifecycleSource{q: q} }

var _ worker.BucketLifecycleSource = (*LifecycleSource)(nil)

func (s *LifecycleSource) ListBucketsWithLifecycle(ctx context.Context) ([]admindomain.Bucket, error) {
	rows, err := s.q.ListBucketsWithLifecycle(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buckets-with-lifecycle: %w", err)
	}
	out := make([]admindomain.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, decodeBucketRow(
			row.BackendName, row.BucketName, row.DisplayName, row.Region, row.Labels,
			row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
			row.ObjectLockEnabled, lockModeFromSQL(row.ObjectLockDefaultMode), row.ObjectLockDefaultRetentionSeconds,
			row.VersioningEnabled, row.VersioningKeepDeletesForever,
			row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
			row.ProvisionState,
			row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
		))
	}
	return out, nil
}

func (s *LifecycleSource) ListCollectionBindings(ctx context.Context, backendID, bucketName string) ([]worker.CollectionBinding, error) {
	rows, err := s.q.ListCollectionBindingsForBucket(ctx, backendID, bucketName)
	if err != nil {
		return nil, fmt.Errorf("list collection bindings: %w", err)
	}
	out := make([]worker.CollectionBinding, 0, len(rows))
	for _, row := range rows {
		out = append(out, worker.CollectionBinding{
			TenantID:   uuidFrom(row.TenantID),
			Collection: row.CollectionName,
		})
	}
	return out, nil
}

// ListBucketsWithReplication satisfies worker.ReplicationSource. Same shape
// as ListBucketsWithLifecycle — different filter predicate.
func (s *LifecycleSource) ListBucketsWithReplication(ctx context.Context) ([]admindomain.Bucket, error) {
	rows, err := s.q.ListBucketsWithReplication(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buckets-with-replication: %w", err)
	}
	out := make([]admindomain.Bucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, decodeBucketRow(
			row.BackendName, row.BucketName, row.DisplayName, row.Region, row.Labels,
			row.OwnerTenantID, row.CedarPolicy, row.Constraints, row.LifecycleRules,
			row.ObjectLockEnabled, lockModeFromSQL(row.ObjectLockDefaultMode), row.ObjectLockDefaultRetentionSeconds,
			row.VersioningEnabled, row.VersioningKeepDeletesForever,
			row.ReplicationEnabled, row.ReplicationDestination, row.ReplicationFilter,
			row.ProvisionState,
			row.ResourceVersion, row.CreatedAt, row.UpdatedAt,
		))
	}
	return out, nil
}

// LifecycleObjectIter implements worker.LifecycleObjectIter via paginated
// reads. Uses object_id-descending cursor so each page is a fresh, point-in-
// time slice of newer-than-last objects (UUIDv7 monotonicity).
type LifecycleObjectIter struct {
	q        *sqlc.Queries
	pageSize int32
}

func NewLifecycleObjectIter(q *sqlc.Queries) *LifecycleObjectIter {
	return &LifecycleObjectIter{q: q, pageSize: 200}
}

var _ worker.LifecycleObjectIter = (*LifecycleObjectIter)(nil)

func (it *LifecycleObjectIter) IterateObjects(ctx context.Context, tenantID uuid.UUID, collection string, cb func(worker.LifecycleObjectRow) error) error {
	var afterID pgtype.UUID
	for {
		rows, err := it.q.IterateObjectsForLifecycle(ctx, pgUUID(tenantID), collection, afterID, it.pageSize)
		if err != nil {
			return fmt.Errorf("iterate objects: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			var size int64
			if row.SizeBytes != nil {
				size = *row.SizeBytes
			}
			err := cb(worker.LifecycleObjectRow{
				ObjectID:    uuidFrom(row.ID),
				State:       string(row.State),
				ContentType: row.ContentType,
				SizeBytes:   size,
				Metadata:    decodeMap(row.Metadata),
				Tags:        decodeMap(row.Tags),
				CreatedAt:   timeFrom(row.CreatedAt),
				CommittedAt: timePtr(row.CommittedAt),
			})
			if err != nil {
				return err
			}
		}
		// Advance cursor to the oldest object_id in this page.
		last := rows[len(rows)-1]
		afterID = last.ID
		// Short page → no more rows.
		if len(rows) < int(it.pageSize) {
			return nil
		}
	}
}
