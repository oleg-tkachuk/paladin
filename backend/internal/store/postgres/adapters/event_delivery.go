package adapters

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// EventDeliveryRepo is the outbox's housekeeping side: retention of the rows
// the dispatcher is done with.
type EventDeliveryRepo struct {
	q *sqlc.Queries
}

// NewEventDeliveryRepo constructs an EventDeliveryRepo over a BYPASSRLS
// querier: retention spans every tenant.
func NewEventDeliveryRepo(q *sqlc.Queries) *EventDeliveryRepo {
	return &EventDeliveryRepo{q: q}
}

// PurgeTerminalBefore deletes up to batchSize delivered or failed rows last
// attempted before cutoff, and returns how many it deleted.
func (r *EventDeliveryRepo) PurgeTerminalBefore(ctx context.Context, cutoff time.Time, batchSize int32) (int64, error) {
	return r.q.PurgeTerminalEventDeliveries(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true}, batchSize)
}
