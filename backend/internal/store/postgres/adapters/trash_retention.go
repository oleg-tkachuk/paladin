package adapters

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TrashRetentionCounter counts the trashed tenants past their trash
// retention, for worker.TrashRetentionGauge.
type TrashRetentionCounter struct{ q *sqlc.Queries }

// NewTrashRetentionCounter wraps q; tenants carries no RLS policy, so any
// role reads every row.
func NewTrashRetentionCounter(q *sqlc.Queries) *TrashRetentionCounter {
	return &TrashRetentionCounter{q: q}
}

// CountTenantsPastTrashRetention is the trashed tenants whose retention
// lapsed at or before asOf.
func (c *TrashRetentionCounter) CountTenantsPastTrashRetention(ctx context.Context, retention time.Duration, asOf time.Time) (int64, error) {
	n, err := c.q.CountTenantsPastTrashRetention(ctx, retention.Seconds(), pgtype.Timestamptz{Time: asOf, Valid: true})
	if err != nil {
		return 0, fmt.Errorf("count tenants past trash retention: %w", err)
	}
	return n, nil
}
