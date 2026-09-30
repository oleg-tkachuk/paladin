package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// ReplicationWatermarkRepo persists per-bucket replication watermarks.
type ReplicationWatermarkRepo struct {
	q *sqlc.Queries
}

func NewReplicationWatermarkRepo(q *sqlc.Queries) *ReplicationWatermarkRepo {
	return &ReplicationWatermarkRepo{q: q}
}

var _ worker.WatermarkStore = (*ReplicationWatermarkRepo)(nil)

// Get returns the stored high-water mark for (backend, bucket). A missing
// row returns zero time + nil error so the worker falls through to its
// LookbackWindow default.
func (r *ReplicationWatermarkRepo) Get(ctx context.Context, backendID, bucketName string) (time.Time, error) {
	row, err := r.q.GetReplicationWatermark(ctx, backendID, bucketName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("get watermark: %w", err)
	}
	return timeFrom(row), nil
}

// Advance writes the watermark. The SQL uses GREATEST() so concurrent
// replicas can't push the mark backwards.
func (r *ReplicationWatermarkRepo) Advance(ctx context.Context, backendID, bucketName string, t time.Time) error {
	return r.q.UpsertReplicationWatermark(ctx, backendID, bucketName, pgTS(t))
}
