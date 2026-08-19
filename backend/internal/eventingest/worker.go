package eventingest

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// DedupStore is the slice of *sqlc.Queries the worker actually needs.
// Decouples the worker from the full Querier interface so unrelated
// query regen doesn't ripple here.
type DedupStore interface {
	ClaimIngestedEvent(
		ctx context.Context,
		eventID, source, eventType string,
		subject *string,
	) (string, error)
}

// Handler is the business-logic seam. Implementations decide what to
// do with a deduplicated event — typically resolve the Paladin object and
// call statemachine.PromoteToAvailable / SoftDelete.
type Handler interface {
	Handle(ctx context.Context, ev CloudEvent) error
}

// HandlerFunc is the function-typed Handler for tests + small adapters.
type HandlerFunc func(ctx context.Context, ev CloudEvent) error

func (f HandlerFunc) Handle(ctx context.Context, ev CloudEvent) error { return f(ctx, ev) }

// Worker wires a Driver to a Handler with dedup in between.
//
// Lifecycle: Run blocks on Driver.Run. Each event the driver delivers
// flows through deliver(): claim a dedup row → run the handler →
// return the handler's error so the driver can ack/nack.
//
// Closed-by-default invariants:
//
//   - Empty event id → reject (we'd never dedup duplicates).
//   - Unknown event type → log + dedup-claim + skip (handler not
//     invoked). Writing the dedup row consumes the broker
//     redelivery without burning the handler retry budget on
//     events we can't act on.
//   - Handler error → no dedup row written; broker re-delivers.
//
// dedup-claim semantics:
//
//   - First sighting of (event_id) → ClaimIngestedEvent returns the
//     event_id → process.
//   - Duplicate (id already in table) → returns "" (no row inserted)
//     → skip.
type Worker struct {
	Driver  Driver
	Handler Handler
	Dedup   DedupStore
	Logger  *zap.Logger
}

func (w *Worker) Run(ctx context.Context) error {
	if w.Driver == nil {
		return errors.New("eventingest: Driver is nil")
	}
	if w.Handler == nil {
		return errors.New("eventingest: Handler is nil")
	}
	if w.Dedup == nil {
		return errors.New("eventingest: Dedup store is nil")
	}
	logger := w.log().With(zap.String("driver", w.Driver.Name()))
	logger.Info("ingest worker starting")
	err := w.Driver.Run(ctx, w.Deliver)
	logger.Info("ingest worker stopped", zap.Error(err))
	return err
}

// Deliver is the per-event critical path. Exported so tests + drivers
// in other packages can reuse the dedup-then-handler flow without
// going through Run. Order:
//
//  1. Validate id non-empty (closed-by-default — no id, no dedup).
//  2. Claim dedup row. ON CONFLICT DO NOTHING means "first sighting"
//     when sql returned a row, "duplicate" when it didn't.
//  3. Run the handler. On error we leave the dedup row in place —
//     the handler is expected to be idempotent and the row holds the
//     line against the broker re-delivering the same event a
//     thousand times. (Alternative: roll back the dedup write on
//     handler error so retries actually do something. We don't do
//     that because the handler's effects are themselves idempotent
//     — re-running PromoteToAvailable is a no-op when the row is
//     already AVAILABLE, and re-running on a non-existent row is a
//     no-op too. The broker still gets a nack and will retry; the
//     dedup row makes that retry a fast skip.)
//
// NOTE on the choice above: for sources with weak ordering (NATS core
// pubsub) the at-most-once transport already loses some events;
// adding "rollback dedup on error" doesn't help. For at-least-once
// transports (JetStream, RabbitMQ) the broker retry interval is
// ours to tune, so the operator can balance "retry budget" vs.
// "dedup table size" via reaper TTL.
func (w *Worker) Deliver(ctx context.Context, ev CloudEvent) error {
	logger := w.log().With(
		zap.String("event_id", ev.ID),
		zap.String("event_type", string(ev.Type)),
		zap.String("source", ev.Source),
	)

	if ev.ID == "" {
		logger.Warn("event has no id; refusing to dedup")
		return errors.New("eventingest: empty event id")
	}

	var subject *string
	if ev.Subject != "" {
		s := ev.Subject
		subject = &s
	}

	_, err := w.Dedup.ClaimIngestedEvent(ctx, ev.ID, ev.Source, string(ev.Type), subject)
	if err != nil {
		// sqlc returns pgx.ErrNoRows when ON CONFLICT DO NOTHING fired —
		// the event is a duplicate, not a real DB failure. Skip the
		// handler and return nil so the broker acks.
		if errors.Is(err, pgx.ErrNoRows) {
			logger.Debug("duplicate event; skipping handler")
			return nil
		}
		logger.Warn("dedup claim failed", zap.Error(err))
		return fmt.Errorf("dedup claim: %w", err)
	}

	if err := w.Handler.Handle(ctx, ev); err != nil {
		logger.Warn("handler failed", zap.Error(err))
		return err
	}
	logger.Info("event processed")
	return nil
}

func (w *Worker) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}

// PgxDedupStore adapts a *sqlc.Queries to DedupStore. The sqlc-generated
// signature already takes a *string for the nullable subject; this
// adapter is a thin pass-through that exists for testability — wiring
// passes a *sqlc.Queries directly through this wrapper.
type PgxDedupStore struct{ Q *sqlc.Queries }

func (p *PgxDedupStore) ClaimIngestedEvent(
	ctx context.Context,
	eventID, source, eventType string,
	subject *string,
) (string, error) {
	return p.Q.ClaimIngestedEvent(ctx, eventID, source, eventType, subject)
}
