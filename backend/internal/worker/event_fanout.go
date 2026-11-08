package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// fanoutSubscription is what the fan-out needs of a subscription: which one,
// what it filters on, and since when it exists.
type fanoutSubscription struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	CELFilter string
	CreatedAt time.Time
}

// recordedEvent is one event_outbox row the fan-out claimed.
type recordedEvent struct {
	id         uuid.UUID
	tenantID   uuid.UUID
	eventType  string
	eventAt    time.Time
	payload    []byte
	recordedAt time.Time
}

// subscribers returns the subscriptions among subs that receive evt, which
// was recorded at recordedAt: those that already existed then and whose
// filter matches (ADR-0044). subs are the tenant's enabled subscriptions.
func (d *Dispatcher) subscribers(subs []fanoutSubscription, evt Event, recordedAt time.Time) []uuid.UUID {
	var out []uuid.UUID
	for _, s := range subs {
		if s.CreatedAt.After(recordedAt) {
			continue
		}
		if !d.subscriptionMatches(admindomain.EventSubscription{
			SubscriptionID: s.ID, TenantID: s.TenantID, CELFilter: s.CELFilter,
		}, evt) {
			continue
		}
		out = append(out, s.ID)
	}
	return out
}

const (
	claimEventsSQL = `
		SELECT id, tenant_id, event_type, event_at, event_payload, created_at
		  FROM event_outbox
		 ORDER BY created_at
		 FOR UPDATE SKIP LOCKED
		 LIMIT $1`
	tenantSubscriptionsSQL = `
		SELECT id, tenant_id, cel_filter, created_at
		  FROM event_subscriptions
		 WHERE tenant_id = $1 AND NOT disabled`
	insertDeliverySQL = `
		INSERT INTO event_deliveries (
			id, tenant_id, subscription_id, event_type, event_at, event_payload
		) VALUES ($1, $2, $3, $4, $5, $6)`
	deleteEventsSQL = `DELETE FROM event_outbox WHERE id = ANY($1)`
)

// FanOut claims a batch of recorded events, writes one event_deliveries row
// per subscription that receives each, and deletes the events — in one
// transaction, so a dispatcher that dies mid-batch leaves the whole batch to
// be fanned out again, never part of it. It returns how many events it
// fanned out.
func (r *OutboxRunner) FanOut(ctx context.Context) (int, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("fan-out: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	events, err := claimEvents(ctx, tx, r.batchSize())
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}
	subsByTenant := map[uuid.UUID][]fanoutSubscription{}
	ids := make([]uuid.UUID, 0, len(events))
	deliveries := 0
	for _, ev := range events {
		ids = append(ids, ev.id)
		var evt Event
		if err := json.Unmarshal(ev.payload, &evt); err != nil {
			// The producer marshalled it; one that does not decode is a defect
			// no retry repairs, so it is dropped rather than claimed forever.
			r.log().Error("recorded event does not decode; dropped without fan-out",
				zap.String("event_id", ev.id.String()),
				zap.String("tenant_id", ev.tenantID.String()),
				zap.String("event_type", ev.eventType),
				zap.Error(err))
			continue
		}
		subs, ok := subsByTenant[ev.tenantID]
		if !ok {
			if subs, err = tenantSubscriptions(ctx, tx, ev.tenantID); err != nil {
				return 0, err
			}
			subsByTenant[ev.tenantID] = subs
		}
		for _, subID := range r.Dispatcher.subscribers(subs, evt, ev.recordedAt) {
			if _, err := tx.Exec(ctx, insertDeliverySQL,
				uuid.NewV7(), ev.tenantID, subID, ev.eventType, ev.eventAt, ev.payload); err != nil {
				return 0, fmt.Errorf("fan-out: event %s to subscription %s: %w", ev.id, subID, err)
			}
			deliveries++
		}
	}
	if _, err := tx.Exec(ctx, deleteEventsSQL, ids); err != nil {
		return 0, fmt.Errorf("fan-out: delete fanned-out events: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("fan-out: commit: %w", err)
	}
	r.log().Debug("events fanned out",
		zap.Int("events", len(events)),
		zap.Int("deliveries", deliveries))
	return len(events), nil
}

func claimEvents(ctx context.Context, tx pgx.Tx, limit int) ([]recordedEvent, error) {
	rows, err := tx.Query(ctx, claimEventsSQL, limit)
	if err != nil {
		return nil, fmt.Errorf("fan-out: claim events: %w", err)
	}
	events, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (recordedEvent, error) {
		var e recordedEvent
		err := row.Scan(&e.id, &e.tenantID, &e.eventType, &e.eventAt, &e.payload, &e.recordedAt)
		return e, err
	})
	if err != nil {
		return nil, fmt.Errorf("fan-out: claim events: %w", err)
	}
	return events, nil
}

func tenantSubscriptions(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) ([]fanoutSubscription, error) {
	rows, err := tx.Query(ctx, tenantSubscriptionsSQL, tenant)
	if err != nil {
		return nil, fmt.Errorf("fan-out: subscriptions of tenant %s: %w", tenant, err)
	}
	subs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (fanoutSubscription, error) {
		var s fanoutSubscription
		err := row.Scan(&s.ID, &s.TenantID, &s.CELFilter, &s.CreatedAt)
		return s, err
	})
	if err != nil {
		return nil, fmt.Errorf("fan-out: subscriptions of tenant %s: %w", tenant, err)
	}
	return subs, nil
}

// FanoutBacklog counts the recorded events not yet fanned out.
func (r *OutboxRunner) FanoutBacklog(ctx context.Context) (int64, error) {
	var n int64
	if err := r.Pool.QueryRow(ctx, `SELECT count(*) FROM event_outbox`).Scan(&n); err != nil {
		return 0, fmt.Errorf("fan-out backlog: %w", err)
	}
	return n, nil
}
