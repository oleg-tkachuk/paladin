package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"
)

// EventProducer is what a worker that announces its work holds: a way to
// record an event on its own transaction. *EventRecorder implements it.
type EventProducer interface {
	RecordTx(ctx context.Context, tx pgx.Tx, tenantID string, evt Event) error
}

// EventRecorder is the producer side of the event outbox (ADR-0044): it
// records an event on the producer's transaction, once, and leaves the
// fan-out to subscriptions to the dispatcher. It reads no subscription and
// takes no connection of its own, so what it adds to the producer's
// transaction does not depend on how many subscriptions the tenant has.
type EventRecorder struct {
	Logger *zap.Logger
}

// NewEventRecorder returns a recorder logging to l; nil logs nothing.
func NewEventRecorder(l *zap.Logger) *EventRecorder {
	return &EventRecorder{Logger: l}
}

// insertEventOutboxSQL records one event. The row's created_at is the
// database's time, which the fan-out compares with each subscription's
// created_at, so both sides of that comparison come from one clock.
const insertEventOutboxSQL = `
	INSERT INTO event_outbox (id, tenant_id, event_type, event_at, event_payload)
	VALUES ($1, $2, $3, $4, $5)
`

// RecordTx records evt for tenantID on tx, so the event commits with the
// change it reports or not at all (ADR-0003). An event with no tenant is
// not recorded: every subscription belongs to a tenant, so none could
// receive it. Any failure is returned; it has aborted the caller's
// transaction, which the caller then rolls back.
func (r *EventRecorder) RecordTx(ctx context.Context, tx pgx.Tx, tenantID string, evt Event) error {
	if tenantID == "" {
		r.log().Debug("event has no tenant, so no subscription can receive it; not recorded",
			zap.String("event_type", evt.Type),
			zap.String("resource_name", evt.ResourceName))
		return nil
	}
	tenant, err := uuid.Parse(tenantID)
	if err != nil {
		return fmt.Errorf("record event %s: invalid tenant id %q: %w", evt.Type, tenantID, err)
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("record event %s: marshal: %w", evt.Type, err)
	}
	if _, err := tx.Exec(ctx, insertEventOutboxSQL, uuid.NewV7(), tenant, evt.Type, evt.At, payload); err != nil {
		return fmt.Errorf("record event %s: %w", evt.Type, err)
	}
	return nil
}

func (r *EventRecorder) log() *zap.Logger {
	if r != nil && r.Logger != nil {
		return r.Logger
	}
	return zap.NewNop()
}
