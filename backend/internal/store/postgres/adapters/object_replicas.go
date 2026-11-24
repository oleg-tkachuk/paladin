package adapters

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// ReplicaRepo is the per-object replication state (object_replicas,
// migration 119) the replication worker claims and records on.
type ReplicaRepo struct {
	q *sqlc.Queries
}

// NewReplicaRepo returns the store over q, which must be a cross-tenant
// connection: the worker serves every tenant's objects.
func NewReplicaRepo(q *sqlc.Queries) *ReplicaRepo {
	return &ReplicaRepo{q: q}
}

var _ worker.ReplicaStore = (*ReplicaRepo)(nil)

// SeedReplication queues a bucket's AVAILABLE objects unless it was seeded
// since its replication settings last changed.
func (r *ReplicaRepo) SeedReplication(ctx context.Context, sourceBucketID pgtype.UUID) (int64, error) {
	n, err := r.q.SeedReplication(ctx, sourceBucketID)
	if err != nil {
		return 0, fmt.Errorf("queue the bucket's objects for replication: %w", err)
	}
	return n, nil
}

// ClaimReplicas claims up to limit due rows of the bucket for lease.
func (r *ReplicaRepo) ClaimReplicas(
	ctx context.Context, sourceBucketID pgtype.UUID, limit int, lease time.Duration,
) ([]worker.ReplicaClaim, error) {
	rows, err := r.q.ClaimReplicas(ctx, pgInterval(lease), sourceBucketID, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("claim due replicas: %w", err)
	}
	out := make([]worker.ReplicaClaim, 0, len(rows))
	for _, row := range rows {
		out = append(out, worker.ReplicaClaim{
			SourceBucketID: sourceBucketID,
			TenantID:       uuidFrom(row.TenantID),
			Collection:     row.CollectionName,
			Key:            row.Path,
			ObjectID:       uuidFrom(row.ObjectID),
			Generation:     row.Generation,
			Attempts:       int(row.Attempts),
			Replica: worker.ReplicaCopy{
				BucketID:  row.ReplicaBucketID,
				ObjectID:  uuidFrom(row.ReplicaObjectID),
				WrittenAt: timeFrom(row.ReplicaWrittenAt),
			},
		})
	}
	return out, nil
}

// ReplicaObjects reads the objects by id as they are now; an id with no row
// is absent from the map.
func (r *ReplicaRepo) ReplicaObjects(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]worker.ReplicaObject, error) {
	pgIDs := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		pgIDs = append(pgIDs, pgUUID(id))
	}
	rows, err := r.q.GetReplicaObjects(ctx, pgIDs)
	if err != nil {
		return nil, fmt.Errorf("read the claimed objects: %w", err)
	}
	out := make(map[uuid.UUID]worker.ReplicaObject, len(rows))
	for _, row := range rows {
		var size int64
		if row.SizeBytes != nil {
			size = *row.SizeBytes
		}
		id := uuidFrom(row.ID)
		out[id] = worker.ReplicaObject{
			Row: worker.LifecycleObjectRow{
				ObjectID:        id,
				Key:             row.Path,
				State:           string(row.State),
				ContentType:     row.ContentType,
				SizeBytes:       size,
				Metadata:        decodeMap(row.Metadata),
				Tags:            decodeMap(row.Tags),
				CreatedAt:       timeFrom(row.CreatedAt),
				UpdatedAt:       timeFrom(row.UpdatedAt),
				CommittedAt:     timePtr(row.CommittedAt),
				ExternalRef:     derefStr(row.ExternalRef),
				ResourceVersion: row.ResourceVersion,
			},
			BucketID:  row.BucketID,
			WrittenAt: timeFrom(row.WrittenAt),
		}
	}
	return out, nil
}

// RecordReplicaCopy records the replica a copy wrote, settling the row at
// the claim's generation.
func (r *ReplicaRepo) RecordReplicaCopy(ctx context.Context, c worker.ReplicaClaim, copied worker.ReplicaCopy) error {
	size := copied.SizeBytes
	if err := r.q.RecordReplicaCopy(ctx, copied.BucketID, pgUUID(copied.ObjectID), pgTS(copied.WrittenAt), &size,
		c.Generation, c.SourceBucketID, pgUUID(c.TenantID), c.Collection, c.Key); err != nil {
		return fmt.Errorf("record the replica: %w", err)
	}
	return nil
}

// SettleReplica settles the row at the claim's generation.
func (r *ReplicaRepo) SettleReplica(ctx context.Context, c worker.ReplicaClaim) error {
	if err := r.q.SettleReplica(ctx, c.Generation, c.SourceBucketID, pgUUID(c.TenantID), c.Collection, c.Key); err != nil {
		return fmt.Errorf("settle the replica: %w", err)
	}
	return nil
}

// RetryReplica records a failed attempt and puts the row off by delay.
func (r *ReplicaRepo) RetryReplica(ctx context.Context, c worker.ReplicaClaim, delay time.Duration, reason string) error {
	if err := r.q.RetryReplica(ctx, c.Generation, reason, pgInterval(delay),
		c.SourceBucketID, pgUUID(c.TenantID), c.Collection, c.Key); err != nil {
		return fmt.Errorf("put the replica off: %w", err)
	}
	return nil
}

// ReleaseReplica hands the claimed row back due.
func (r *ReplicaRepo) ReleaseReplica(ctx context.Context, c worker.ReplicaClaim) error {
	if err := r.q.ReleaseReplica(ctx, c.SourceBucketID, pgUUID(c.TenantID), c.Collection, c.Key); err != nil {
		return fmt.Errorf("release the replica: %w", err)
	}
	return nil
}

// ForgetReplica drops the row at the claim's generation. A row a later write
// took over is the new object's: it is handed back due instead.
func (r *ReplicaRepo) ForgetReplica(ctx context.Context, c worker.ReplicaClaim) error {
	n, err := r.q.ForgetReplica(ctx, c.SourceBucketID, pgUUID(c.TenantID), c.Collection, c.Key, c.Generation)
	if err != nil {
		return fmt.Errorf("forget the replica: %w", err)
	}
	if n == 0 {
		return r.ReleaseReplica(ctx, c)
	}
	return nil
}

// pgInterval is d as a Postgres interval.
func pgInterval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}
